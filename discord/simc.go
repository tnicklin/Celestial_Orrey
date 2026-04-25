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
	if c.simcBiB == nil || c.simcQueue == nil {
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

	requester := m.Author.Username
	if m.Member != nil && m.Member.Nick != "" {
		requester = m.Member.Nick
	}

	// Use the next BiB ID for the thread name (it'll match the actual one
	// since Submit assigns IDs monotonically and we hold no other ones).
	threadID, threadName, threadOK := c.openBiBThread(m.ChannelID, m.ID, requester)

	id, err := c.simcBiB.Submit(profile, requester,
		func(info simc.BiBRunInfo) { c.postBiBProgress(threadID, info) },
		func(info simc.BiBRunInfo, res *simc.BiBResult, runErr error) {
			c.postBiBOutcome(threadID, info, res, runErr)
		},
	)
	if err != nil {
		return cmdResponse{}, err
	}

	c.postBiBIntro(threadID, id, requester, profile, stats)

	if threadOK {
		return cmdResponse{content: fmt.Sprintf("Started **BiB #%d** in thread **%s**.", id, threadName)}, nil
	}
	// Threads not available; tell the user we'll post inline.
	return cmdResponse{content: fmt.Sprintf("Started **BiB #%d** (threads unavailable; posting inline).", id)}, nil
}

// openBiBThread starts a public thread off the user's command message.
// Returns the threadID + name when successful; on failure it returns the
// channel ID and a false ok flag so callers fall back to inline posts.
func (c *DefaultDiscord) openBiBThread(channelID, messageID string, requester string) (string, string, bool) {
	name := fmt.Sprintf("BiB %s — %s", requester, time.Now().Format("Jan 2 15:04"))
	if len(name) > 95 {
		name = name[:95] // Discord caps thread names at 100 chars
	}
	thread, err := c.session.MessageThreadStartComplex(channelID, messageID, &discordgo.ThreadStart{
		Name:                name,
		AutoArchiveDuration: 4320, // 3 days
	})
	if err != nil {
		c.logger.WarnW("create simc thread", "channel", channelID, "error", err)
		return channelID, "", false
	}
	return thread.ID, name, true
}

// postBiBIntro sends the first message in a BiB thread: a summary plus the
// original input file as an attachment so the run is self-documenting.
func (c *DefaultDiscord) postBiBIntro(threadID string, id simc.BiBRunID, requester string, profile []byte, stats simc.CombinationStats) {
	body := fmt.Sprintf(
		"**BiB #%d** for **%s**\n%d candidate combinations × 2 fight styles (Patchwerk + Dungeon Slice)",
		id, requester, stats.Total,
	)
	if w := bibCandidateWarnings(stats); w != "" {
		body = body + "\n\n" + w
	}
	send := &discordgo.MessageSend{
		Content: body,
		Files: []*discordgo.File{{
			Name:        fmt.Sprintf("bib-%d-input.simc", id),
			ContentType: "text/plain",
			Reader:      bytes.NewReader(profile),
		}},
	}
	if _, err := c.session.ChannelMessageSendComplex(threadID, send); err != nil {
		c.logger.WarnW("post simc intro", "thread", threadID, "error", err)
	}
}

func bibCandidateWarnings(stats simc.CombinationStats) string {
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

func (c *DefaultDiscord) postBiBProgress(channelID string, info simc.BiBRunInfo) {
	msg := fmt.Sprintf("BiB **#%d** (%s) — %s · %d/%d sims",
		info.ID, info.Requester, info.Phase, info.CompletedSims, info.TotalSims)
	if err := c.WriteMessage(channelID, msg); err != nil {
		c.logger.WarnW("post bib progress", "run", info.ID, "error", err)
	}
}

func (c *DefaultDiscord) postBiBOutcome(channelID string, info simc.BiBRunInfo, res *simc.BiBResult, runErr error) {
	if runErr != nil {
		_ = c.WriteMessage(channelID, fmt.Sprintf("BiB **#%d** (%s) %s: `%s`",
			info.ID, info.Requester, info.Status, truncate(runErr.Error(), 500)))
		return
	}
	if res == nil {
		_ = c.WriteMessage(channelID, fmt.Sprintf("BiB **#%d** (%s) finished but produced no result.", info.ID, info.Requester))
		return
	}
	embed := buildBiBEmbed(info, res)
	send := &discordgo.MessageSend{
		Content: fmt.Sprintf("BiB **#%d** complete for **%s**.", info.ID, info.Requester),
		Embeds:  []*discordgo.MessageEmbed{embed},
	}
	if _, err := c.session.ChannelMessageSendComplex(channelID, send); err != nil {
		c.logger.ErrorW("post bib outcome", "run", info.ID, "error", err)
	}
}

func buildBiBEmbed(info simc.BiBRunInfo, res *simc.BiBResult) *discordgo.MessageEmbed {
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
			sb.WriteString("  " + formatSlotChange(ch) + "\n")
		}
		sb.WriteString("\n")
	}
	if len(dsChanges) > 0 {
		sb.WriteString("Dungeon Slice slot changes:\n")
		for _, ch := range dsChanges {
			sb.WriteString("  " + formatSlotChange(ch) + "\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("Combinations: %d  ·  Total sims: %d  ·  Duration: %s\n",
		res.CombinationCount, info.TotalSims, info.Duration.Round(time.Second)))
	sb.WriteString("```")
	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("Best in Bags #%d — %s", info.ID, info.Requester),
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

func formatSlotChange(ch simc.SlotChange) string {
	return fmt.Sprintf("%-9s  %s  →  %s", ch.Slot, formatItemList(ch.Current), formatItemList(ch.Best))
}

func formatItemList(items []simc.Item) string {
	if len(items) == 0 {
		return "(empty)"
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		name := it.Name
		if name == "" {
			name = fmt.Sprintf("id:%d", it.ItemID)
		}
		parts = append(parts, fmt.Sprintf("%s [%d]", name, it.OriginalIlvl))
	}
	return strings.Join(parts, ", ")
}

func (c *DefaultDiscord) cmdSimcStatus() (cmdResponse, error) {
	bibSnap := c.simcBiB.Stats()
	queueSnap := c.simcQueue.Stats()

	if bibSnap.Running == nil && len(bibSnap.Pending) == 0 && queueSnap.Running == nil && len(queueSnap.Queued) == 0 {
		return cmdResponse{content: "SimC is idle."}, nil
	}

	var sb strings.Builder
	sb.WriteString("```\n")
	if bibSnap.Running != nil {
		r := bibSnap.Running
		sb.WriteString(fmt.Sprintf("BiB running: #%d  %s\n", r.ID, r.Requester))
		sb.WriteString(fmt.Sprintf("             %s · %d/%d sims · %s elapsed\n",
			r.Phase, r.CompletedSims, r.TotalSims, time.Since(r.StartedAt).Round(time.Second)))
	}
	if len(bibSnap.Pending) > 0 {
		sb.WriteString(fmt.Sprintf("BiB queued: %d\n", len(bibSnap.Pending)))
		for _, p := range bibSnap.Pending {
			sb.WriteString(fmt.Sprintf("   #%d  %s  (waiting %s)\n",
				p.ID, p.Requester, time.Since(p.SubmittedAt).Round(time.Second)))
		}
	}

	if queueSnap.Running != nil {
		j := queueSnap.Running
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
	bibSnap := c.simcBiB.Stats()
	queueSnap := c.simcQueue.Stats()

	var sb strings.Builder
	sb.WriteString("```\n")
	if bibSnap.Running != nil {
		r := bibSnap.Running
		sb.WriteString(fmt.Sprintf("BiB:    #%d  %s\n", r.ID, r.Requester))
		sb.WriteString(fmt.Sprintf("        %s · %d/%d sims · %s elapsed\n",
			r.Phase, r.CompletedSims, r.TotalSims, time.Since(r.StartedAt).Round(time.Second)))
		if r.BestPatchwerk > 0 {
			sb.WriteString(fmt.Sprintf("        best PW so far: %s\n", formatDPS(r.BestPatchwerk)))
		}
		if r.BestDungeonSlice > 0 {
			sb.WriteString(fmt.Sprintf("        best DS so far: %s\n", formatDPS(r.BestDungeonSlice)))
		}
	} else if len(bibSnap.Pending) == 0 {
		sb.WriteString("BiB:    (idle)\n")
	}

	if queueSnap.Running != nil {
		j := queueSnap.Running
		sb.WriteString(fmt.Sprintf("Sim:    #%d  %s  %s  %d iters  %s elapsed\n",
			j.ID, j.Requester, j.FightStyle, j.Iterations,
			time.Since(j.StartedAt).Round(time.Second)))
		if queueSnap.Process != nil {
			sb.WriteString(fmt.Sprintf("        pid %d  cpu %.0f%%  rss %s  threads %d\n",
				queueSnap.Process.PID, queueSnap.Process.CPUPercent,
				humanBytes(queueSnap.Process.RSSBytes), queueSnap.Process.ThreadCount,
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

	if len(bibSnap.Recent) > 0 {
		sb.WriteString("Recent BiB runs:\n")
		for _, r := range bibSnap.Recent {
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
		return cmdResponse{content: "Usage: `!simc cancel <bib_id>`"}, nil
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(args[0], "#"), 10, 64)
	if err != nil {
		return cmdResponse{content: fmt.Sprintf("Bad ID %q.", args[0])}, nil
	}
	if err := c.simcBiB.Cancel(simc.BiBRunID(id)); err != nil {
		if errors.Is(err, simc.ErrJobNotFound) {
			return cmdResponse{content: fmt.Sprintf("No active BiB run #%d.", id)}, nil
		}
		return cmdResponse{}, err
	}
	return cmdResponse{content: fmt.Sprintf("Cancel requested for BiB #%d.", id)}, nil
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
	return `**SimC Best-in-Bags Commands**` + "\n```" + `
!simc run
    Attach your /simc dump (or paste it directly — Discord auto-converts
    large pastes into a message.txt attachment).
    Runs Best-in-Bags across all hero/myth bag combinations, sim'd at both
    Patchwerk and Dungeon Slice. Hero items are rescaled to 276, myth to 289.
    Gems and enchants are stripped for an apples-to-apples comparison.
    Long runtime expected — minutes to hours depending on candidate count.

!simc status         Show BiB run + sim queue state
!simc stats          Detailed runtime + container resource stats
!simc cancel <id>    Cancel a queued or running BiB run
!simc help           Show this message
` + "```"
}
