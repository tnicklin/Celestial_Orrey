package discord

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/tnicklin/celestial_orrey/simc"
)

const (
	_simcRun    = "run"
	_simcStatus = "status"
	_simcStats  = "stats"
	_simcCancel = "cancel"
	_simcHelp   = "help"
)

// downloadTimeout caps how long we wait for an attachment to come down from
// the Discord CDN. Profiles are tiny.
const downloadTimeout = 30 * time.Second

func (c *DefaultDiscord) cmdSimc(ctx context.Context, m *discordgo.MessageCreate, args []string) (cmdResponse, error) {
	if c.simcOrch == nil || c.simcQueue == nil {
		return cmdResponse{content: "SimC is not configured on this bot."}, nil
	}
	if len(args) == 0 {
		return cmdResponse{content: simcUsage()}, nil
	}
	switch strings.ToLower(args[0]) {
	case _simcRun:
		return c.cmdSimcRun(ctx, m)
	case _simcStatus:
		return c.cmdSimcStatus()
	case _simcStats:
		return c.cmdSimcStats()
	case _simcCancel:
		return c.cmdSimcCancel(args[1:])
	case _simcHelp:
		return cmdResponse{content: simcUsage()}, nil
	default:
		return cmdResponse{content: simcUsage()}, nil
	}
}

func (c *DefaultDiscord) cmdSimcRun(ctx context.Context, m *discordgo.MessageCreate) (cmdResponse, error) {
	if len(m.Attachments) == 0 {
		return cmdResponse{content: "Attach your `/simc` profile (or paste it directly — Discord auto-converts large pastes into a `message.txt` attachment)."}, nil
	}

	att := m.Attachments[0]
	if int64(att.Size) > c.simcConfig.MaxProfileBytes {
		return cmdResponse{content: fmt.Sprintf("Attachment is %s; max allowed is %s.",
			humanBytes(uint64(att.Size)), humanBytes(uint64(c.simcConfig.MaxProfileBytes)))}, nil
	}
	if !looksLikeSimcAttachment(att.Filename) {
		return cmdResponse{content: fmt.Sprintf("Attachment %q does not look like a simc profile (expected .simc, .txt, or no extension).", att.Filename)}, nil
	}

	profile, err := downloadAttachment(ctx, att.URL, c.simcConfig.MaxProfileBytes)
	if err != nil {
		return cmdResponse{}, fmt.Errorf("download attachment: %w", err)
	}
	parsed, err := simc.ParseProfile(profile)
	if err != nil {
		return cmdResponse{content: fmt.Sprintf("Profile rejected: %v", err)}, nil
	}
	stats := simc.AnalyzeCandidates(parsed.CandidatesBySlot())

	// Prime the name resolver with anything the addon comment already
	// gave us — saves a wowhead round trip later.
	for _, it := range parsed.Items {
		if it.Name != "" && it.ItemID != 0 {
			c.simcNames.Prime(ctx, it.ItemID, it.Name)
		}
	}

	requester := m.Author.Username
	if m.Member != nil && m.Member.Nick != "" {
		requester = m.Member.Nick
	}

	// Use the next ID for the thread name (it'll match the actual one
	// since Submit assigns IDs monotonically and we hold no other ones).
	threadID, threadOK := c.openSimThread(m.ChannelID, m.ID, parsed)

	id, err := c.simcOrch.Submit(profile, requester,
		func(info simc.RunInfo) { c.postSimProgress(threadID, info) },
		func(info simc.RunInfo, res *simc.RunResult, runErr error) {
			c.postSimOutcome(threadID, info, res, runErr)
		},
	)
	if err != nil {
		return cmdResponse{}, err
	}

	c.postSimIntro(threadID, id, parsed, profile, stats)

	// On thread success, the new thread + its intro post are signal enough.
	// Only reply inline if we couldn't open a thread.
	if threadOK {
		return cmdResponse{}, nil
	}
	return cmdResponse{content: fmt.Sprintf("Started **sim #%d** (threads unavailable; posting inline).", id)}, nil
}

// openSimThread starts a public thread off the user's command message.
// On any failure (no permission, unsupported channel) returns the
// channel ID and ok=false so callers fall back to inline posts.
func (c *DefaultDiscord) openSimThread(channelID, messageID string, p *simc.Profile) (string, bool) {
	name := simThreadName(p)
	thread, err := c.session.MessageThreadStartComplex(channelID, messageID, &discordgo.ThreadStart{
		Name:                name,
		AutoArchiveDuration: 4320, // 3 days
	})
	if err != nil {
		c.logger.WarnW("create simc thread", "channel", channelID, "error", err)
		return channelID, false
	}
	return thread.ID, true
}

// simThreadName builds a thread name from the parsed profile. Falls back to
// a generic "sim -- <time>" if the character/realm aren't extractable.
func simThreadName(p *simc.Profile) string {
	char := strings.ToLower(p.CharacterName())
	realm := strings.ToLower(p.Realm())
	stamp := time.Now().Format("Jan 2 15:04")
	var name string
	switch {
	case char != "" && realm != "":
		name = fmt.Sprintf("%s-%s -- %s", char, realm, stamp)
	case char != "":
		name = fmt.Sprintf("%s -- %s", char, stamp)
	default:
		name = fmt.Sprintf("sim -- %s", stamp)
	}
	if len(name) > 95 {
		name = name[:95] // Discord caps at 100 chars
	}
	return name
}

// postSimIntro sends the first message in a sim thread: a summary plus the
// original input file as an attachment so the run is self-documenting.
func (c *DefaultDiscord) postSimIntro(threadID string, id simc.RunID, p *simc.Profile, profile []byte, stats simc.CombinationStats) {
	header := fmt.Sprintf("**Sim #%d**", id)
	if char := p.CharacterName(); char != "" {
		header += fmt.Sprintf(" — %s", char)
		if spec := p.Spec(); spec != "" {
			header += fmt.Sprintf(" (%s %s)", spec, p.ClassName())
		}
		if realm := p.Realm(); realm != "" {
			header += fmt.Sprintf(" — %s", realm)
		}
	}
	candidateCount := 0
	slotCount := 0
	for _, items := range stats.BySlot {
		if items > 0 {
			candidateCount += items
			slotCount++
		}
	}
	body := fmt.Sprintf(
		"%s\n%d candidate items across %d slots × 2 fight styles (Patchwerk + Dungeon Slice)",
		header, candidateCount, slotCount,
	)
	if w := simCandidateWarnings(stats); w != "" {
		body = body + "\n\n" + w
	}
	send := &discordgo.MessageSend{
		Content: body,
		Files: []*discordgo.File{{
			Name:        fmt.Sprintf("sim-%d-input.simc", id),
			ContentType: "text/plain",
			Reader:      bytes.NewReader(profile),
		}},
	}
	if _, err := c.session.ChannelMessageSendComplex(threadID, send); err != nil {
		c.logger.WarnW("post simc intro", "thread", threadID, "error", err)
	}
}

func simCandidateWarnings(stats simc.CombinationStats) string {
	if len(stats.Empty) == 0 && len(stats.DoubleEmpty) == 0 {
		return ""
	}
	var parts []string
	if len(stats.Empty) > 0 {
		names := make([]string, 0, len(stats.Empty))
		for _, s := range stats.Empty {
			names = append(names, s.String())
		}
		parts = append(parts, fmt.Sprintf("no hero/myth candidates for: %s", strings.Join(names, ", ")))
	}
	if len(stats.DoubleEmpty) > 0 {
		names := make([]string, 0, len(stats.DoubleEmpty))
		for _, s := range stats.DoubleEmpty {
			names = append(names, fmt.Sprintf("%s (%d)", s.String(), stats.BySlot[s]))
		}
		parts = append(parts, fmt.Sprintf("fewer than 2 candidates for: %s", strings.Join(names, ", ")))
	}
	return "Warnings: " + strings.Join(parts, "; ") + "."
}

func (c *DefaultDiscord) postSimProgress(channelID string, info simc.RunInfo) {
	msg := fmt.Sprintf("sim **#%d** (%s) — %s · %d/%d sims",
		info.ID, info.DisplayName(), info.Phase, info.CompletedSims, info.TotalSims)
	if err := c.WriteMessage(channelID, msg); err != nil {
		c.logger.WarnW("post sim progress", "run", info.ID, "error", err)
	}
}

func (c *DefaultDiscord) postSimOutcome(channelID string, info simc.RunInfo, res *simc.RunResult, runErr error) {
	if runErr != nil {
		_ = c.WriteMessage(channelID, fmt.Sprintf("sim **#%d** (%s) %s: `%s`",
			info.ID, info.DisplayName(), info.Status, truncate(runErr.Error(), 500)))
		return
	}
	if res == nil {
		_ = c.WriteMessage(channelID, fmt.Sprintf("sim **#%d** (%s) finished but produced no result.", info.ID, info.DisplayName()))
		return
	}
	embed := buildSimEmbed(c, info, res)
	send := &discordgo.MessageSend{
		Content: fmt.Sprintf("sim **#%d** complete for **%s**.", info.ID, info.DisplayName()),
		Embeds:  []*discordgo.MessageEmbed{embed},
	}
	if _, err := c.session.ChannelMessageSendComplex(channelID, send); err != nil {
		c.logger.ErrorW("post sim outcome", "run", info.ID, "error", err)
	}
}

func buildSimEmbed(c *DefaultDiscord, info simc.RunInfo, res *simc.RunResult) *discordgo.MessageEmbed {
	var sb strings.Builder
	sb.WriteString("```\n")
	writeFightSummary(&sb, "Patchwerk    ", res.Patchwerk)
	sb.WriteString("\n")
	writeFightSummary(&sb, "Dungeon Slice", res.DungeonSlice)
	sb.WriteString("\n")

	pwChanges := changedSlots(res.Patchwerk.SlotChanges)
	dsChanges := changedSlots(res.DungeonSlice.SlotChanges)

	if len(pwChanges) > 0 {
		sb.WriteString("Patchwerk slot changes:\n")
		for _, ch := range pwChanges {
			sb.WriteString("  " + c.formatSlotChange(ch) + "\n")
		}
		sb.WriteString("\n")
	}
	if len(dsChanges) > 0 {
		sb.WriteString("Dungeon Slice slot changes:\n")
		for _, ch := range dsChanges {
			sb.WriteString("  " + c.formatSlotChange(ch) + "\n")
		}
		sb.WriteString("\n")
	}
	if len(res.Patchwerk.GemChanges) > 0 {
		sb.WriteString("Patchwerk gem changes:\n")
		for _, ch := range res.Patchwerk.GemChanges {
			sb.WriteString("  " + c.formatGemChange(ch) + "\n")
		}
		sb.WriteString("\n")
	}
	if len(res.Patchwerk.EnchantChanges) > 0 {
		sb.WriteString("Patchwerk enchant changes:\n")
		for _, ch := range res.Patchwerk.EnchantChanges {
			sb.WriteString("  " + c.formatEnchantChange(ch) + "\n")
		}
		sb.WriteString("\n")
	}
	if len(res.DungeonSlice.GemChanges) > 0 {
		sb.WriteString("Dungeon Slice gem changes:\n")
		for _, ch := range res.DungeonSlice.GemChanges {
			sb.WriteString("  " + c.formatGemChange(ch) + "\n")
		}
		sb.WriteString("\n")
	}
	if len(res.DungeonSlice.EnchantChanges) > 0 {
		sb.WriteString("Dungeon Slice enchant changes:\n")
		for _, ch := range res.DungeonSlice.EnchantChanges {
			sb.WriteString("  " + c.formatEnchantChange(ch) + "\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("Candidates: %d  ·  Total sims: %d  ·  Duration: %s\n",
		res.CandidateCount, info.TotalSims, info.Duration.Round(time.Second)))
	sb.WriteString("```")
	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("Sim #%d — %s", info.ID, info.DisplayName()),
		Description: sb.String(),
		Color:       embedColor,
	}
}

func writeFightSummary(sb *strings.Builder, label string, r simc.FightStyleResult) {
	sb.WriteString(fmt.Sprintf("%s\n", label))
	sb.WriteString(fmt.Sprintf("   Current: %12s\n", formatDPS(r.BaselineDPS)))
	sb.WriteString(fmt.Sprintf("   Best:    %12s   (%s%s / %+.2f%%)\n",
		formatDPS(r.BestDPS), deltaSign(r.DeltaDPS), formatDPS(absFloat(r.DeltaDPS)), r.DeltaPct))
}

func deltaSign(d float64) string {
	if d >= 0 {
		return "+"
	}
	return "-"
}

func absFloat(d float64) float64 {
	if d < 0 {
		return -d
	}
	return d
}

func changedSlots(changes []simc.SlotChange) []simc.SlotChange {
	out := make([]simc.SlotChange, 0, len(changes))
	for _, ch := range changes {
		if ch.Changed {
			out = append(out, ch)
		}
	}
	return out
}

// formatSlotChange renders a slot diff across two lines so long item
// names don't wrap awkwardly in the embed:
//   back       Shroud of the Soulhunter [276]
//              ↳ Fluxweave Cloak [263]
// The slot label appears on the first line; the indent on line 2 keeps
// the arrow lined up under the item names.
func (c *DefaultDiscord) formatSlotChange(ch simc.SlotChange) string {
	const slotW = 9
	indent := strings.Repeat(" ", slotW+2) // slotW + 2-space gap to align under name col
	return fmt.Sprintf("%-*s  %s\n%s↳ %s",
		slotW, ch.Slot,
		c.formatItemList(ch.Current),
		indent,
		c.formatItemList(ch.Best),
	)
}

// formatGemChange renders a gem swap on two lines with the same ↳
// indent style as slot changes:
//   ring 1     Flawless Crit (id:240892)
//              ↳ Flawless Haste (id:240891)
func (c *DefaultDiscord) formatGemChange(ch simc.GemChange) string {
	const slotW = 9
	indent := strings.Repeat(" ", slotW+2)
	before := formatGemRef(ch.Before, "")
	after := formatGemRef(ch.After, ch.Name)
	return fmt.Sprintf("%-*s  %s\n%s↳ %s",
		slotW, ch.Slot, before, indent, after,
	)
}

// formatEnchantChange renders a ring-enchant swap on two lines.
func (c *DefaultDiscord) formatEnchantChange(ch simc.EnchantChange) string {
	const slotW = 9
	indent := strings.Repeat(" ", slotW+2)
	before := formatEnchantRef(ch.Before, "")
	after := formatEnchantRef(ch.After, ch.Name)
	return fmt.Sprintf("%-*s  %s\n%s↳ %s",
		slotW, ch.Slot, before, indent, after,
	)
}

// formatGemRef formats a gem_id value with a name when one is known.
// Slash-separated multi-socket strings are passed through verbatim
// after the first id (e.g. "240892/240891").
func formatGemRef(raw, name string) string {
	if raw == "" {
		return "(none)"
	}
	if name != "" {
		return fmt.Sprintf("%s (id:%s)", name, raw)
	}
	return fmt.Sprintf("id:%s", raw)
}

func formatEnchantRef(raw, name string) string {
	if raw == "" {
		return "(none)"
	}
	if name != "" {
		return fmt.Sprintf("%s (id:%s)", name, raw)
	}
	return fmt.Sprintf("id:%s", raw)
}

func (c *DefaultDiscord) formatItemList(items []simc.Item) string {
	if len(items) == 0 {
		return "(empty)"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	parts := make([]string, 0, len(items))
	for _, it := range items {
		name := it.Name
		if name == "" && c.simcNames != nil {
			name = c.simcNames.Resolve(ctx, it.ItemID)
		}
		if name == "" {
			name = fmt.Sprintf("id:%d", it.ItemID)
		}
		parts = append(parts, fmt.Sprintf("%s [%d]", name, it.OriginalIlvl))
	}
	return strings.Join(parts, ", ")
}

func (c *DefaultDiscord) cmdSimcStatus() (cmdResponse, error) {
	orchSnap := c.simcOrch.Stats()
	queueSnap := c.simcQueue.Stats()

	if orchSnap.Running == nil && len(orchSnap.Pending) == 0 && len(queueSnap.Running) == 0 && len(queueSnap.Queued) == 0 {
		return cmdResponse{content: "SimC is idle."}, nil
	}

	var sb strings.Builder
	sb.WriteString("```\n")
	if orchSnap.Running != nil {
		r := orchSnap.Running
		sb.WriteString(fmt.Sprintf("Sim running: #%d  %s\n", r.ID, r.DisplayName()))
		sb.WriteString(fmt.Sprintf("             %s · %d/%d sims · %s elapsed\n",
			r.Phase, r.CompletedSims, r.TotalSims, time.Since(r.StartedAt).Round(time.Second)))
	}
	if len(orchSnap.Pending) > 0 {
		sb.WriteString(fmt.Sprintf("Sim queued: %d\n", len(orchSnap.Pending)))
		for _, p := range orchSnap.Pending {
			sb.WriteString(fmt.Sprintf("   #%d  %s  (waiting %s)\n",
				p.ID, p.DisplayName(), time.Since(p.SubmittedAt).Round(time.Second)))
		}
	}

	for _, j := range queueSnap.Running {
		sb.WriteString(fmt.Sprintf("Sim running: #%d  %s  %s  %d iters\n",
			j.ID, j.Requester, j.FightStyle, j.Iterations))
	}
	if len(queueSnap.Queued) > 0 {
		sb.WriteString(fmt.Sprintf("Sim queued:  %d / %d\n", queueSnap.QueueDepth, queueSnap.QueueCap))
	}
	sb.WriteString("```")
	return cmdResponse{content: sb.String()}, nil
}

func (c *DefaultDiscord) cmdSimcStats() (cmdResponse, error) {
	orchSnap := c.simcOrch.Stats()
	queueSnap := c.simcQueue.Stats()

	var sb strings.Builder
	sb.WriteString("```\n")
	if orchSnap.Running != nil {
		r := orchSnap.Running
		sb.WriteString(fmt.Sprintf("Sim:    #%d  %s\n", r.ID, r.DisplayName()))
		sb.WriteString(fmt.Sprintf("        %s · %d/%d sims · %s elapsed\n",
			r.Phase, r.CompletedSims, r.TotalSims, time.Since(r.StartedAt).Round(time.Second)))
		if r.BestPatchwerk > 0 {
			sb.WriteString(fmt.Sprintf("        best PW so far: %s\n", formatDPS(r.BestPatchwerk)))
		}
		if r.BestDungeonSlice > 0 {
			sb.WriteString(fmt.Sprintf("        best DS so far: %s\n", formatDPS(r.BestDungeonSlice)))
		}
	} else if len(orchSnap.Pending) == 0 {
		sb.WriteString("Sim:    (idle)\n")
	}

	for i, j := range queueSnap.Running {
		sb.WriteString(fmt.Sprintf("Sim:    #%d  %s  %s  %d iters  %s elapsed\n",
			j.ID, j.Requester, j.FightStyle, j.Iterations,
			time.Since(j.StartedAt).Round(time.Second)))
		if i < len(queueSnap.Processes) {
			ps := queueSnap.Processes[i]
			sb.WriteString(fmt.Sprintf("        pid %d  cpu %.0f%%  rss %s  threads %d\n",
				ps.PID, ps.CPUPercent, humanBytes(ps.RSSBytes), ps.ThreadCount,
			))
		}
	}

	sb.WriteString("\n")
	cpuLine := fmt.Sprintf("Container: %.2f / %.2f cores", queueSnap.Container.CPUUsageCores, queueSnap.Container.CPUQuotaCores)
	if queueSnap.Container.CPUQuotaCores == 0 {
		cpuLine = fmt.Sprintf("Container: %.2f cores in use (no cap)", queueSnap.Container.CPUUsageCores)
	}
	memLine := fmt.Sprintf("           %s / %s", humanBytes(queueSnap.Container.MemUsedBytes), humanBytes(queueSnap.Container.MemLimitBytes))
	if queueSnap.Container.MemLimitBytes == 0 {
		memLine = fmt.Sprintf("           %s in use (no cap)", humanBytes(queueSnap.Container.MemUsedBytes))
	}
	sb.WriteString(cpuLine + "\n")
	sb.WriteString(memLine + "\n")

	sb.WriteString(fmt.Sprintf("\nLifetime sims:  %d ok · %d failed · %d canceled\n",
		queueSnap.TotalCompleted, queueSnap.TotalFailed, queueSnap.TotalCanceled))

	if len(orchSnap.Recent) > 0 {
		sb.WriteString("Recent sim runs:\n")
		for _, r := range orchSnap.Recent {
			sb.WriteString(fmt.Sprintf("   #%-4d  %-8s  %-10s  %-15s  PW %s  DS %s\n",
				r.ID, r.Status, r.Duration.Round(time.Second), r.Requester,
				formatDPS(r.BestPatchwerk), formatDPS(r.BestDungeonSlice),
			))
		}
	}
	sb.WriteString("```")

	embed := &discordgo.MessageEmbed{
		Title:       "SimC Status",
		Description: sb.String(),
		Color:       embedColor,
	}
	return cmdResponse{embeds: []*discordgo.MessageEmbed{embed}}, nil
}

func (c *DefaultDiscord) cmdSimcCancel(args []string) (cmdResponse, error) {
	if len(args) == 0 {
		return cmdResponse{content: "Usage: `!simc cancel <id>`"}, nil
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(args[0], "#"), 10, 64)
	if err != nil {
		return cmdResponse{content: fmt.Sprintf("Bad ID %q.", args[0])}, nil
	}
	if err := c.simcOrch.Cancel(simc.RunID(id)); err != nil {
		if errors.Is(err, simc.ErrJobNotFound) {
			return cmdResponse{content: fmt.Sprintf("No active sim #%d.", id)}, nil
		}
		return cmdResponse{}, err
	}
	return cmdResponse{content: fmt.Sprintf("Cancel requested for sim #%d.", id)}, nil
}

func looksLikeSimcAttachment(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case "", ".simc", ".txt", ".profile":
		return true
	}
	return false
}

func downloadAttachment(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	dlCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
}

func formatDPS(d float64) string {
	switch {
	case d >= 1_000_000:
		return fmt.Sprintf("%.2fM", d/1_000_000)
	case d >= 1_000:
		return fmt.Sprintf("%.1fK", d/1_000)
	default:
		return fmt.Sprintf("%.0f", d)
	}
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func simcUsage() string {
	return `**SimC sim Commands**` + "\n```" + `
!simc run
    Attach your /simc dump (or paste it directly — Discord auto-converts
    large pastes into a message.txt attachment).
    Greedy per-slot search across hero/myth bag candidates, sim'd at both
    Patchwerk and Dungeon Slice. Hero items are rescaled to 276, myth to 289.
    Gems and enchants are kept verbatim from the paste.
    Runtime grows with candidate count — typically minutes, not hours.

!simc status         Show Active sims and queue state
!simc stats          Detailed runtime + container resource stats
!simc cancel <id>    Cancel a queued or running sim
!simc help           Show this message
` + "```"
}
