package discord

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/tnicklin/celestial_orrey/clock"
	"github.com/tnicklin/celestial_orrey/logger"
	"github.com/tnicklin/celestial_orrey/models"
	rioClient "github.com/tnicklin/celestial_orrey/raiderio/client"
	"github.com/tnicklin/celestial_orrey/store"
	"github.com/tnicklin/celestial_orrey/timeutil"
	"github.com/tnicklin/celestial_orrey/warcraftlogs"
	"go.uber.org/zap"
)

const commandPrefix = "!"

// embedColor is the Mythic+ themed purple used for all embeds.
const embedColor = 0x9B59B6

// cmdResponse holds the response from a command handler.
// Either content (plain text) or embeds (rich embed) should be set.
type cmdResponse struct {
	content string
	embeds  []*discordgo.MessageEmbed
}

var _pstLocation = timeutil.Location()

var _ Discord = (*DefaultDiscord)(nil)

type DefaultDiscord struct {
	session       *discordgo.Session
	guildID       string
	listenChannel string
	store         store.Store
	raiderIO      rioClient.Client
	warcraftLogs  warcraftlogs.WCL
	logger        logger.Logger
	clock         clock.Clock
	removeHandler func()
	stopScheduler chan struct{}
	schedulerDone chan struct{}
}

type Params struct {
	Config       Config
	Store        store.Store
	RaiderIO     rioClient.Client
	WarcraftLogs warcraftlogs.WCL
	Logger       logger.Logger
	Clock        clock.Clock
}

func New(p Params) (*DefaultDiscord, error) {
	cfg := p.Config

	session, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}

	clk := p.Clock
	if clk == nil {
		clk = clock.System()
	}

	return &DefaultDiscord{
		session:       session,
		guildID:       cfg.GuildID,
		listenChannel: cfg.ListenChannel,
		store:         p.Store,
		raiderIO:      p.RaiderIO,
		warcraftLogs:  p.WarcraftLogs,
		logger:        p.Logger,
		clock:         clk,
	}, nil
}

func (c *DefaultDiscord) Start(ctx context.Context) error {
	if err := c.session.Open(); err != nil {
		return fmt.Errorf("open discord connection: %w", err)
	}

	c.removeHandler = c.session.AddHandler(c.handleMessage)
	c.stopScheduler = make(chan struct{})
	c.schedulerDone = make(chan struct{})

	go c.runScheduler()

	return nil
}

func (c *DefaultDiscord) Stop() {
	if c.removeHandler != nil {
		c.removeHandler()
		c.removeHandler = nil
	}
	if c.stopScheduler != nil {
		close(c.stopScheduler)
		<-c.schedulerDone
	}
	c.session.Close()
}

func (c *DefaultDiscord) runScheduler() {
	defer close(c.schedulerDone)

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	var lastPost time.Time
	for {
		select {
		case <-c.stopScheduler:
			return
		case <-ticker.C:
			now := c.clock.Now()
			pstNow := now.In(_pstLocation)

			if pstNow.Hour() == 7 && pstNow.Minute() == 0 {
				today := time.Date(pstNow.Year(), pstNow.Month(), pstNow.Day(), 7, 0, 0, 0, _pstLocation)
				if !today.Equal(lastPost) {
					lastPost = today
					c.postDailyAnnouncement(pstNow)
				}
			}
		}
	}
}

func (c *DefaultDiscord) postDailyAnnouncement(now time.Time) {
	ctx := context.Background()

	if now.Weekday() == time.Tuesday {
		if err := c.store.ArchiveWeek(ctx); err != nil {
			c.logger.ErrorW("archive week", "error", err)
		}

		msg := "**Dawn of the 1st Day**"
		if err := c.WriteMessage(c.listenChannel, msg); err != nil {
			c.logger.ErrorW("post reset message", "error", err)
		}
		return
	}

	resetTime := timeutil.WeeklyResetAt(c.clock.Now())
	resp, err := c.formatAllCharactersReport(ctx, resetTime)
	if err != nil {
		c.logger.ErrorW("generate report", "error", err)
		return
	}

	if len(resp.embeds) > 0 {
		if _, err := c.session.ChannelMessageSendComplex(c.listenChannel, &discordgo.MessageSend{
			Embeds: resp.embeds,
		}); err != nil {
			c.logger.ErrorW("post daily report", "error", err)
		}
	} else if resp.content != "" {
		if err := c.WriteMessage(c.listenChannel, resp.content); err != nil {
			c.logger.ErrorW("post daily report", "error", err)
		}
	}
}

const (
	_cmdKeys   = "keys"
	_cmdReport = "report"
	_cmdChar   = "char"
	_cmdElv    = "elv"
	_cmdHelp   = "help"
)

const (
	_askr  = "askr_"
	_xtein = "xtein"
)

func (c *DefaultDiscord) handleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot {
		return
	}

	if c.listenChannel != "" && m.ChannelID != c.listenChannel {
		return
	}

	if !strings.HasPrefix(m.Content, commandPrefix) {
		return
	}

	if m.Author.Username == _xtein {
		err := s.MessageReactionAdd(m.ChannelID, m.ID, "✅")
		if err != nil {
			c.logger.ErrorW("react", zap.Error(err))
		}
	}

	content := strings.TrimPrefix(m.Content, commandPrefix)
	parts := strings.Fields(content)
	if len(parts) == 0 {
		return
	}

	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	var (
		resp cmdResponse
		err  error
	)

	ctx := context.Background()
	switch cmd {
	case _cmdKeys:
		resp, err = c.cmdKeys(ctx, args)
	case _cmdReport:
		resp, err = c.cmdReport(ctx, args)
	case _cmdChar:
		var s string
		s, err = c.cmdChar(ctx, args)
		resp = cmdResponse{content: s}
	case _cmdElv:
		resp, err = c.cmdElv(ctx)
	case _cmdHelp:
		resp = cmdResponse{content: c.cmdHelp()}
	default:
		return
	}
	if err != nil {
		c.logger.ErrorW("command failed", "command", cmd, "error", err)
		resp = cmdResponse{content: fmt.Sprintf("Error: %v", err)}
	}

	if len(resp.embeds) > 0 {
		if _, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
			Embeds: resp.embeds,
		}); err != nil {
			c.logger.ErrorW("failed to send response", "error", err)
		}
	} else if resp.content != "" {
		if _, err := s.ChannelMessageSend(m.ChannelID, resp.content); err != nil {
			c.logger.ErrorW("failed to send response", "error", err)
		}
	}
}

// cmdKeys handles the !keys command
// Usage: !keys [character_name]
func (c *DefaultDiscord) cmdKeys(ctx context.Context, args []string) (cmdResponse, error) {
	if c.store == nil {
		return cmdResponse{}, errors.New("database not configured")
	}

	resetTime := timeutil.WeeklyResetAt(c.clock.Now())

	if len(args) == 0 {
		return cmdResponse{content: "Usage: `!keys <character_name>` or `!keys all`\nExample: `!keys askrm` or `!keys all`"}, nil
	}

	if strings.ToLower(args[0]) == "all" {
		return c.formatAllCharacterKeys(ctx, resetTime)
	}

	return c.formatCharacterKeys(ctx, args[0], resetTime)
}

func (c *DefaultDiscord) formatCharacterKeys(ctx context.Context, query string, since time.Time) (cmdResponse, error) {
	allChars, err := c.store.ListCharacters(ctx)
	if err != nil {
		return cmdResponse{}, err
	}

	var matchingChars []models.Character
	queryLower := strings.ToLower(query)

	for _, char := range allChars {
		charKey := strings.ToLower(char.Name + "-" + char.Realm)
		if charKey == queryLower {
			matchingChars = append(matchingChars, char)
		}
	}

	if len(matchingChars) == 0 {
		for _, char := range allChars {
			if strings.ToLower(char.Name) == queryLower {
				matchingChars = append(matchingChars, char)
			}
		}
	}

	if len(matchingChars) == 0 {
		return cmdResponse{content: fmt.Sprintf("No character found matching **%s**.", query)}, nil
	}

	if len(matchingChars) > 1 {
		var realms []string
		for _, char := range matchingChars {
			realms = append(realms, char.Realm)
		}
		return cmdResponse{content: fmt.Sprintf("Ambiguous character name **%s** found on multiple realms: %s\nPlease use `!keys <name>-<realm>` to specify.", query, strings.Join(realms, ", "))}, nil
	}

	char := matchingChars[0]
	keys, err := c.store.ListKeysByCharacterSince(ctx, char.Name, since)
	if err != nil {
		return cmdResponse{}, err
	}

	var charKeys []models.CompletedKey
	for _, key := range keys {
		if strings.EqualFold(key.Realm, char.Realm) && strings.EqualFold(key.Region, char.Region) {
			charKeys = append(charKeys, key)
		}
	}

	if len(charKeys) == 0 {
		return cmdResponse{content: fmt.Sprintf("No keys found for **%s** (%s) this week.", char.Name, char.Realm)}, nil
	}

	keyWord := "keys"
	if len(charKeys) == 1 {
		keyWord = "key"
	}

	var sb strings.Builder
	c.writeKeyLines(ctx, &sb, charKeys)

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("%s (%s) — %d %s", char.Name, char.Realm, len(charKeys), keyWord),
		Description: fmt.Sprintf("Week of %s\n\n%s", since.Format("Jan 2"), sb.String()),
		Color:       embedColor,
	}

	return cmdResponse{embeds: []*discordgo.MessageEmbed{embed}}, nil
}

func (c *DefaultDiscord) formatAllCharacterKeys(ctx context.Context, since time.Time) (cmdResponse, error) {
	allChars, err := c.store.ListCharacters(ctx)
	if err != nil {
		return cmdResponse{}, err
	}

	// Sort characters by name for deterministic output
	sort.Slice(allChars, func(i, j int) bool {
		return allChars[i].Name < allChars[j].Name
	})

	if len(allChars) == 0 {
		return cmdResponse{content: "No characters in database."}, nil
	}

	embed := &discordgo.MessageEmbed{
		Title:       "Keys since reset",
		Description: fmt.Sprintf("Week of %s", since.Format("Jan 2")),
		Color:       embedColor,
	}

	for _, char := range allChars {
		keys, err := c.store.ListKeysByCharacterSince(ctx, char.Name, since)
		if err != nil {
			continue
		}

		var charKeys []models.CompletedKey
		for _, key := range keys {
			if strings.EqualFold(key.Realm, char.Realm) && strings.EqualFold(key.Region, char.Region) {
				charKeys = append(charKeys, key)
			}
		}

		if len(charKeys) == 0 {
			continue
		}

		keyWord := "keys"
		if len(charKeys) == 1 {
			keyWord = "key"
		}

		var sb strings.Builder
		c.writeKeyLines(ctx, &sb, charKeys)

		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  fmt.Sprintf("%s (%s) — %d %s", char.Name, char.Realm, len(charKeys), keyWord),
			Value: sb.String(),
		})
	}

	if len(embed.Fields) == 0 {
		return cmdResponse{content: "No keys completed this week."}, nil
	}

	return cmdResponse{embeds: []*discordgo.MessageEmbed{embed}}, nil
}

// writeKeyLines writes individual key lines to a string builder.
func (c *DefaultDiscord) writeKeyLines(ctx context.Context, sb *strings.Builder, keys []models.CompletedKey) {
	for _, key := range keys {
		completedAt := formatShortTime(key.CompletedAt)
		dungeonShort := shortenDungeonName(key.Dungeon)
		timing := formatTimingDiff(key.RunTimeMS, key.ParTimeMS)

		wclLink := ""
		links, err := c.store.ListWarcraftLogsLinksForKey(ctx, key.KeyID)
		if err == nil && len(links) > 0 {
			wclLink = fmt.Sprintf(" [log](<%s>)", links[0].URL)
		}

		sb.WriteString(fmt.Sprintf("%s  +%d %s  %s%s\n", completedAt, key.KeyLevel, dungeonShort, timing, wclLink))
	}
}

// cmdReport handles the !report command for weekly vault progress.
// Usage: !report [character_name]
func (c *DefaultDiscord) cmdReport(ctx context.Context, args []string) (cmdResponse, error) {
	resetTime := timeutil.WeeklyResetAt(c.clock.Now())

	if len(args) > 0 {
		return c.formatCharacterReport(ctx, args[0], resetTime)
	}

	return c.formatAllCharactersReport(ctx, resetTime)
}

func (c *DefaultDiscord) formatCharacterReport(ctx context.Context, name string, since time.Time) (cmdResponse, error) {
	allChars, err := c.store.ListCharacters(ctx)
	if err != nil {
		return cmdResponse{}, err
	}

	var matchingChars []models.Character
	nameLower := strings.ToLower(name)
	for _, char := range allChars {
		if strings.ToLower(char.Name) == nameLower {
			matchingChars = append(matchingChars, char)
		}
	}

	if len(matchingChars) == 0 {
		return cmdResponse{content: fmt.Sprintf("No character found with name **%s**.", name)}, nil
	}

	sort.Slice(matchingChars, func(i, j int) bool {
		return matchingChars[i].Realm < matchingChars[j].Realm
	})

	embed := &discordgo.MessageEmbed{
		Title:       "Great Vault Progress",
		Description: fmt.Sprintf("Week of %s\n%s", since.Format("Jan 2"), c.buildReportBlock(ctx, matchingChars, since)),
		Color:       embedColor,
	}

	return cmdResponse{embeds: []*discordgo.MessageEmbed{embed}}, nil
}

func (c *DefaultDiscord) formatAllCharactersReport(ctx context.Context, since time.Time) (cmdResponse, error) {
	allChars, err := c.store.ListCharacters(ctx)
	if err != nil {
		return cmdResponse{}, err
	}

	sort.Slice(allChars, func(i, j int) bool {
		return allChars[i].Name < allChars[j].Name
	})

	if len(allChars) == 0 {
		return cmdResponse{content: "No characters in database."}, nil
	}

	embed := &discordgo.MessageEmbed{
		Title:       "Great Vault Progress",
		Description: fmt.Sprintf("Week of %s\n%s", since.Format("Jan 2"), c.buildReportBlock(ctx, allChars, since)),
		Color:       embedColor,
	}

	return cmdResponse{embeds: []*discordgo.MessageEmbed{embed}}, nil
}

type reportEntry struct {
	label    string
	keyCount int
	vault1   string
	vault2   string
	vault3   string
}

// buildReportBlock collects character data and formats it as an aligned code block.
func (c *DefaultDiscord) buildReportBlock(ctx context.Context, chars []models.Character, since time.Time) string {
	var entries []reportEntry
	maxLabelLen := 0

	for _, char := range chars {
		keys, err := c.store.ListKeysByCharacterSince(ctx, char.Name, since)
		if err != nil {
			c.logger.ErrorW("list keys for character", "character", char.Name, "error", err)
			continue
		}

		var charKeys []models.CompletedKey
		for _, key := range keys {
			if strings.EqualFold(key.Realm, char.Realm) && strings.EqualFold(key.Region, char.Region) {
				charKeys = append(charKeys, key)
			}
		}

		sortKeysByLevel(charKeys)

		label := char.Name
		if char.RIOScore > 0 {
			label = fmt.Sprintf("%s (%.1f)", char.Name, char.RIOScore)
		}
		if len(label) > maxLabelLen {
			maxLabelLen = len(label)
		}

		entries = append(entries, reportEntry{
			label:    label,
			keyCount: len(charKeys),
			vault1:   getVaultSlotPlain(charKeys, 0),
			vault2:   getVaultSlotPlain(charKeys, 3),
			vault3:   getVaultSlotPlain(charKeys, 7),
		})
	}

	var sb strings.Builder
	sb.WriteString("```\n")
	for _, e := range entries {
		keyWord := "keys"
		if e.keyCount == 1 {
			keyWord = "key "
		}
		sb.WriteString(fmt.Sprintf("%-*s  %2d %s  %s %s %s\n",
			maxLabelLen, e.label, e.keyCount, keyWord, e.vault1, e.vault2, e.vault3))
	}
	sb.WriteString("```")
	return sb.String()
}

// getVaultSlotPlain returns a fixed-width plain text vault slot for code blocks.
func getVaultSlotPlain(keys []models.CompletedKey, index int) string {
	if index >= len(keys) {
		return EmptySlotDisplay()
	}
	return VaultRewards.GetVaultSlotDisplay(keys[index].KeyLevel)
}

// sortKeysByLevel sorts keys by KeyLevel descending (highest first)
func sortKeysByLevel(keys []models.CompletedKey) {
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].KeyLevel > keys[j].KeyLevel
	})
}

func (c *DefaultDiscord) cmdElv(ctx context.Context) (cmdResponse, error) {
	if c.store == nil {
		return cmdResponse{}, errors.New("database not configured")
	}

	v, err := c.store.GetElvUIVersion(ctx)
	if err != nil {
		return cmdResponse{content: "No ElvUI version data available yet."}, nil
	}

	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("ElvUI %s", v.Version),
		Description: fmt.Sprintf("[Download](%s)\nLast updated: %s\n[Changelog](%s)",
			v.DownloadURL, v.LastUpdate, v.ChangelogURL),
		Color: embedColor,
	}

	return cmdResponse{embeds: []*discordgo.MessageEmbed{embed}}, nil
}

func (c *DefaultDiscord) cmdHelp() string {
	return `**Available Commands:**
` + "```" + `
!keys <name>               - Show keys for a character
!keys all                  - Show all keys completed this week
!report                    - Show Great Vault progress for all characters
!report <name>             - Show Great Vault progress for a character
!char sync <name> <realm>  - Sync character from RaiderIO
!char purge <name> <realm> - Remove character from database
!elv                       - Show current ElvUI version
!help                      - Show this help message
` + "```"
}

const (
	_cmdSync  = "sync"
	_cmdPurge = "purge"
)

// cmdChar handles character management commands.
func (c *DefaultDiscord) cmdChar(ctx context.Context, args []string) (string, error) {
	if len(args) < 1 {
		return "Usage: `!char sync <name> <realm>` or `!char purge <name> <realm>`", nil
	}

	subCmd := strings.ToLower(args[0])
	subArgs := args[1:]

	switch subCmd {
	case _cmdSync:
		return c.cmdCharSync(ctx, subArgs)
	case _cmdPurge:
		return c.cmdCharPurge(ctx, subArgs)
	default:
		return "Unknown subcommand. Use `sync` or `purge`.", nil
	}
}

// cmdCharSync syncs a character from RaiderIO and links WarcraftLogs
func (c *DefaultDiscord) cmdCharSync(ctx context.Context, args []string) (string, error) {
	if c.store == nil {
		return "", errors.New("database not configured")
	}
	if c.raiderIO == nil {
		return "", errors.New("RaiderIO client not configured")
	}

	if len(args) < 2 {
		return "Usage: `!char sync <name> <realm>`\nExample: `!char sync Askrm malganis`\nUse realm slugs (e.g., area-52, burning-legion)", nil
	}

	char := models.Character{
		Name:   strings.ToLower(args[0]),
		Realm:  strings.ToLower(args[1]),
		Region: "us",
	}

	result, err := c.raiderIO.FetchWeeklyRuns(ctx, char)
	if err != nil {
		return "", fmt.Errorf("fetch from RaiderIO: %w", err)
	}

	// Update character's RIO score
	_ = c.store.UpdateCharacterScore(ctx, char.Name, char.Realm, char.Region, result.RIOScore)

	insertedCount := 0
	for _, key := range result.Keys {
		if err := c.store.UpsertCompletedKey(ctx, key); err == nil {
			insertedCount++
		}
	}

	linkedCount := 0
	if c.warcraftLogs != nil {
		linker := warcraftlogs.NewLinker(warcraftlogs.LinkerParams{
			Store:  c.store,
			Client: c.warcraftLogs,
		})
		linker.MatchWindow = 24 * time.Hour

		for _, key := range result.Keys {
			existingLinks, _ := c.store.ListWarcraftLogsLinksForKey(ctx, key.KeyID)
			if len(existingLinks) > 0 {
				continue
			}

			match, err := linker.MatchKey(ctx, key)
			if err != nil || match == nil {
				continue
			}

			url := warcraftlogs.BuildMythicPlusURL(match.Run)
			fightID := int64(match.Run.FightID)

			link := store.WarcraftLogsLink{
				KeyID:      key.KeyID,
				ReportCode: match.Run.ReportCode,
				FightID:    &fightID,
				URL:        url,
			}
			if err := c.store.UpsertWarcraftLogsLink(ctx, link); err == nil {
				linkedCount++
			}
		}
	}

	scoreStr := ""
	if result.RIOScore > 0 {
		scoreStr = fmt.Sprintf(" | RIO Score: **%.1f**", result.RIOScore)
	}

	return fmt.Sprintf("Synced **%s** (%s-%s): %d keys fetched, %d inserted, %d WCL links created.%s",
		char.Name, char.Realm, char.Region, len(result.Keys), insertedCount, linkedCount, scoreStr), nil
}

// cmdCharPurge removes a character and all their data from the database
func (c *DefaultDiscord) cmdCharPurge(ctx context.Context, args []string) (string, error) {
	if c.store == nil {
		return "", errors.New("database not configured")
	}

	if len(args) < 2 {
		return "Usage: `!char purge <name> <realm>`\nExample: `!char purge Askrm malganis`\nUse realm slugs (e.g., area-52, burning-legion)", nil
	}

	name := strings.ToLower(args[0])
	realm := strings.ToLower(args[1])
	region := "us"

	_, err := c.store.GetCharacter(ctx, name, realm, region)
	if err != nil {
		return fmt.Sprintf("Character **%s** (%s-%s) not found in database.", name, realm, region), nil
	}

	if err := c.store.DeleteCharacter(ctx, name, realm, region); err != nil {
		return "", fmt.Errorf("failed to delete character: %w", err)
	}

	return fmt.Sprintf("Purged **%s** (%s-%s) and all associated data from database.", name, realm, region), nil
}

func formatShortTime(completedAt string) string {
	t, err := timeutil.ParseRFC3339(completedAt)
	if err != nil {
		return completedAt
	}
	return t.In(_pstLocation).Format("Mon 3:04pm")
}

func formatTimingDiff(runTimeMS, parTimeMS int64) string {
	if runTimeMS <= 0 || parTimeMS <= 0 {
		return ""
	}

	diff := runTimeMS - parTimeMS
	sign := "+"
	if diff < 0 {
		sign = "-"
		diff = -diff
	}

	mins := diff / 60000
	secs := (diff % 60000) / 1000

	return fmt.Sprintf("(%s%d:%02d)", sign, mins, secs)
}

/*
func shortenDungeonName(dungeon string) string {
	replacements := map[string]string{
		"Magisters’ Terrace":      "Magisters",
		"Maisara Caverns":         "Maisara",
		"Nexus Point Xenas":       "Nexus Point",
		"Windrunner Spire":        "Windrunner",
		"Algeth’ar Academy":       "Algeth’ar",
		"Seat of the Triumvirate": "Triumvirate",
		"Skyreach":                "Skyreach",
		"Pit of Saron":            "Saron",
	}

	if short, ok := replacements[dungeon]; ok {
		return short
	}
	return dungeon
}
*/

func shortenDungeonName(dungeon string) string {
	replacements := map[string]string{
		"Operation: Floodgate":        "Floodgate",
		"Ara-Kara, City of Echoes":    "Ara-Kara",
		"The Dawnbreaker":             "Dawnbreaker",
		"Priory of the Sacred Flame":  "Priory",
		"Eco-Dome Al'dani":            "Eco-Dome",
		"Tazavesh: Streets of Wonder": "Streets",
		"Tazavesh: So'leah's Gambit":  "Gambit",
		"Halls of Atonement":          "Halls",
	}

	if short, ok := replacements[dungeon]; ok {
		return short
	}
	return dungeon
}

func (c *DefaultDiscord) WriteMessage(channelID, msg string) error {
	if c.session == nil {
		return errors.New("discord session is nil")
	}
	_, err := c.session.ChannelMessageSend(channelID, msg)
	return err
}
