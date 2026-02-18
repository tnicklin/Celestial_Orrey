package discord

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/tnicklin/celestial_orrey/logger"
	"github.com/tnicklin/celestial_orrey/models"
	rioClient "github.com/tnicklin/celestial_orrey/raiderio/client"
	"github.com/tnicklin/celestial_orrey/store"
	"github.com/tnicklin/celestial_orrey/timeutil"
	"github.com/tnicklin/celestial_orrey/warcraftlogs"
	"go.uber.org/zap"
)

const commandPrefix = "!"

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
}

func New(p Params) (*DefaultDiscord, error) {
	cfg := p.Config

	session, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}

	return &DefaultDiscord{
		session:       session,
		guildID:       cfg.GuildID,
		listenChannel: cfg.ListenChannel,
		store:         p.Store,
		raiderIO:      p.RaiderIO,
		warcraftLogs:  p.WarcraftLogs,
		logger:        p.Logger,
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
		case now := <-ticker.C:
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

	resetTime := timeutil.WeeklyReset()
	report, err := c.formatAllCharactersReport(ctx, resetTime)
	if err != nil {
		c.logger.ErrorW("generate report", "error", err)
		return
	}

	if err := c.WriteMessage(c.listenChannel, report); err != nil {
		c.logger.ErrorW("post daily report", "error", err)
	}
}

const (
	_cmdKeys   = "keys"
	_cmdReport = "report"
	_cmdChar   = "char"
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
		response string
		err      error
	)

	ctx := context.Background()
	switch cmd {
	case _cmdKeys:
		response, err = c.cmdKeys(ctx, args)
	case _cmdReport:
		response, err = c.cmdReport(ctx, args)
	case _cmdChar:
		response, err = c.cmdChar(ctx, args)
	case _cmdHelp:
		response = c.cmdHelp()
	default:
		return
	}
	if err != nil {
		c.logger.ErrorW("command failed", "command", cmd, "error", err)
		response = fmt.Sprintf("Error: %v", err)
	}

	if response != "" {
		if _, err := s.ChannelMessageSend(m.ChannelID, response); err != nil {
			c.logger.ErrorW("failed to send response", "error", err)
		}
	}
}

// cmdKeys handles the !keys command
// Usage: !keys [character_name]
func (c *DefaultDiscord) cmdKeys(ctx context.Context, args []string) (string, error) {
	if c.store == nil {
		return "", errors.New("database not configured")
	}

	resetTime := timeutil.WeeklyReset()

	if len(args) == 0 {
		return "Usage: `!keys <character_name>` or `!keys all`\nExample: `!keys askrm` or `!keys all`", nil
	}

	if strings.ToLower(args[0]) == "all" {
		return c.formatAllCharacterKeys(ctx, resetTime)
	}

	return c.formatCharacterKeys(ctx, args[0], resetTime)
}

func (c *DefaultDiscord) formatCharacterKeys(ctx context.Context, query string, since time.Time) (string, error) {
	allChars, err := c.store.ListCharacters(ctx)
	if err != nil {
		return "", err
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
		return fmt.Sprintf("No character found matching **%s**.", query), nil
	}

	if len(matchingChars) > 1 {
		var realms []string
		for _, char := range matchingChars {
			realms = append(realms, char.Realm)
		}
		return fmt.Sprintf("Ambiguous character name **%s** found on multiple realms: %s\nPlease use `!keys <name>-<realm>` to specify.", query, strings.Join(realms, ", ")), nil
	}

	char := matchingChars[0]
	keys, err := c.store.ListKeysByCharacterSince(ctx, char.Name, since)
	if err != nil {
		return "", err
	}

	var charKeys []models.CompletedKey
	for _, key := range keys {
		if strings.EqualFold(key.Realm, char.Realm) && strings.EqualFold(key.Region, char.Region) {
			charKeys = append(charKeys, key)
		}
	}

	if len(charKeys) == 0 {
		return fmt.Sprintf("No keys found for **%s** (%s) this week.", char.Name, char.Realm), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Keys since reset** (Week of %s)\n\n", since.Format("Jan 2")))
	c.writeCharacterSection(ctx, &sb, char, charKeys)

	return sb.String(), nil
}

func (c *DefaultDiscord) formatAllCharacterKeys(ctx context.Context, since time.Time) (string, error) {
	allChars, err := c.store.ListCharacters(ctx)
	if err != nil {
		return "", err
	}

	// Sort characters by name for deterministic output
	sort.Slice(allChars, func(i, j int) bool {
		return allChars[i].Name < allChars[j].Name
	})

	if len(allChars) == 0 {
		return "No characters in database.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Keys since reset** (Week of %s)\n\n", since.Format("Jan 2")))

	hasKeys := false
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

		hasKeys = true
		c.writeCharacterSection(ctx, &sb, char, charKeys)
	}

	if !hasKeys {
		return "No keys completed this week.", nil
	}

	return sb.String(), nil
}

func (c *DefaultDiscord) writeCharacterSection(ctx context.Context, sb *strings.Builder, char models.Character, keys []models.CompletedKey) {
	keyWord := "keys"
	if len(keys) == 1 {
		keyWord = "key"
	}
	sb.WriteString(fmt.Sprintf("**%s** (%s) — %d %s\n", char.Name, char.Realm, len(keys), keyWord))

	for _, key := range keys {
		completedAt := formatShortTime(key.CompletedAt)
		dungeonShort := shortenDungeonName(key.Dungeon)
		timing := formatTimingDiff(key.RunTimeMS, key.ParTimeMS)

		wclLink := ""
		links, err := c.store.ListWarcraftLogsLinksForKey(ctx, key.KeyID)
		if err == nil && len(links) > 0 {
			wclLink = fmt.Sprintf(" [log](<%s>)", links[0].URL)
		}

		sb.WriteString(fmt.Sprintf("• [%s] %d %s %s%s\n", completedAt, key.KeyLevel, dungeonShort, timing, wclLink))
	}
	sb.WriteString("\n")
}

// cmdReport handles the !report command for weekly vault progress.
// Usage: !report [character_name]
func (c *DefaultDiscord) cmdReport(ctx context.Context, args []string) (string, error) {
	resetTime := timeutil.WeeklyReset()

	if len(args) > 0 {
		return c.formatCharacterReport(ctx, args[0], resetTime)
	}

	return c.formatAllCharactersReport(ctx, resetTime)
}

func (c *DefaultDiscord) formatCharacterReport(ctx context.Context, name string, since time.Time) (string, error) {
	allChars, err := c.store.ListCharacters(ctx)
	if err != nil {
		return "", err
	}

	var matchingChars []models.Character
	nameLower := strings.ToLower(name)
	for _, char := range allChars {
		if strings.ToLower(char.Name) == nameLower {
			matchingChars = append(matchingChars, char)
		}
	}

	if len(matchingChars) == 0 {
		return fmt.Sprintf("No character found with name **%s**.", name), nil
	}

	sort.Slice(matchingChars, func(i, j int) bool {
		return matchingChars[i].Realm < matchingChars[j].Realm
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Great Vault Progress** (Week of %s)\n", since.Format("Jan 2")))
	sb.WriteString("```ansi\n")

	for _, char := range matchingChars {
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
		c.writeReportLine(&sb, char.Name, len(charKeys), charKeys)
	}

	sb.WriteString("```")

	return sb.String(), nil
}

func (c *DefaultDiscord) formatAllCharactersReport(ctx context.Context, since time.Time) (string, error) {
	allChars, err := c.store.ListCharacters(ctx)
	if err != nil {
		return "", err
	}

	sort.Slice(allChars, func(i, j int) bool {
		return allChars[i].Name < allChars[j].Name
	})

	if len(allChars) == 0 {
		return "No characters in database.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Great Vault Progress** (Week of %s)\n", since.Format("Jan 2")))
	sb.WriteString("```ansi\n")

	maxNameLen := 0
	for _, char := range allChars {
		if len(char.Name) > maxNameLen {
			maxNameLen = len(char.Name)
		}
	}

	hasKeys := false
	for _, char := range allChars {
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

		if len(charKeys) == 0 {
			continue
		}

		hasKeys = true
		sortKeysByLevel(charKeys)
		c.writeReportLineAligned(&sb, char.Name, maxNameLen, len(charKeys), charKeys)
	}

	sb.WriteString("```")

	if !hasKeys {
		return "No keys completed this week yet.", nil
	}

	return sb.String(), nil
}

func (c *DefaultDiscord) writeReportLine(sb *strings.Builder, name string, keyCount int, keys []models.CompletedKey) {
	vault1 := getVaultSlotColored(keys, 0)
	vault2 := getVaultSlotColored(keys, 3)
	vault3 := getVaultSlotColored(keys, 7)

	sb.WriteString(fmt.Sprintf("%s: %d keys %s %s %s\n",
		name, keyCount, vault1, vault2, vault3))
}

func (c *DefaultDiscord) writeReportLineAligned(sb *strings.Builder, name string, maxNameLen, keyCount int, keys []models.CompletedKey) {
	vault1 := getVaultSlotColored(keys, 0)
	vault2 := getVaultSlotColored(keys, 3)
	vault3 := getVaultSlotColored(keys, 7)

	paddedName := name + strings.Repeat(" ", maxNameLen-len(name))
	paddedKeys := fmt.Sprintf("%d", keyCount)
	if keyCount < 10 {
		paddedKeys = " " + paddedKeys
	}

	sb.WriteString(fmt.Sprintf("%s: %s keys %s %s %s\n",
		paddedName, paddedKeys, vault1, vault2, vault3))
}

// sortKeysByLevel sorts keys by KeyLevel descending (highest first)
func sortKeysByLevel(keys []models.CompletedKey) {
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].KeyLevel > keys[j].KeyLevel
	})
}

// getVaultSlotColored returns the colored vault slot display for ANSI code blocks
func getVaultSlotColored(keys []models.CompletedKey, index int) string {
	if index >= len(keys) {
		return EmptySlotDisplayColored()
	}
	keyLevel := keys[index].KeyLevel
	return VaultRewards.GetVaultSlotDisplayColored(keyLevel)
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

	keys, err := c.raiderIO.FetchWeeklyRuns(ctx, char)
	if err != nil {
		return "", fmt.Errorf("fetch from RaiderIO: %w", err)
	}

	insertedCount := 0
	for _, key := range keys {
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

		for _, key := range keys {
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

	return fmt.Sprintf("Synced **%s** (%s-%s): %d keys fetched, %d inserted, %d WCL links created.",
		char.Name, char.Realm, char.Region, len(keys), insertedCount, linkedCount), nil
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
