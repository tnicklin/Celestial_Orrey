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
)

var _ Discord = (*DefaultDiscord)(nil)

const (
	commandPrefix = "!"
	adminRoleName = "bot-admin"
)

// pstLocation is the timezone for formatting times.
var pstLocation = timeutil.Location()

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

	log := p.Logger
	if log == nil {
		log = logger.NewNop()
	}

	return &DefaultDiscord{
		session:       session,
		guildID:       cfg.GuildID,
		listenChannel: cfg.ListenChannel,
		store:         p.Store,
		raiderIO:      p.RaiderIO,
		warcraftLogs:  p.WarcraftLogs,
		logger:        log,
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
			pstNow := now.In(pstLocation)

			// 7am PST daily post
			if pstNow.Hour() == 7 && pstNow.Minute() == 0 {
				today := time.Date(pstNow.Year(), pstNow.Month(), pstNow.Day(), 7, 0, 0, 0, pstLocation)
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

	// Tuesday = reset day
	if now.Weekday() == time.Tuesday {
		// Archive last week's data before reset
		if err := c.store.ArchiveWeek(ctx); err != nil {
			c.logger.ErrorW("failed to archive week", "error", err)
		}

		// Post reset message
		msg := "**Dawn of the 1st Day**"
		if err := c.WriteMessage(c.listenChannel, msg); err != nil {
			c.logger.ErrorW("failed to post reset message", "error", err)
		}
		return
	}

	// Other days: post the weekly progress report
	resetTime := timeutil.WeeklyReset()
	report, err := c.formatAllCharactersReport(ctx, resetTime)
	if err != nil {
		c.logger.ErrorW("failed to generate report", "error", err)
		return
	}

	if err := c.WriteMessage(c.listenChannel, report); err != nil {
		c.logger.ErrorW("failed to post daily report", "error", err)
	}
}

func (c *DefaultDiscord) handleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot {
		return
	}

	// Only respond in the configured listen channel
	if c.listenChannel != "" && m.ChannelID != c.listenChannel {
		return
	}

	if !strings.HasPrefix(m.Content, commandPrefix) {
		return
	}

	content := strings.TrimPrefix(m.Content, commandPrefix)
	parts := strings.Fields(content)
	if len(parts) == 0 {
		return
	}

	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	ctx := context.Background()

	var response string
	var err error

	switch cmd {
	case "keys":
		response, err = c.cmdKeys(ctx, args)
	case "report":
		response, err = c.cmdReport(ctx, args)
	case "help":
		response = c.cmdHelp()
	case "char":
		response, err = c.cmdChar(ctx, s, m, args)
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

	// "all" shows all characters
	if strings.ToLower(args[0]) == "all" {
		return c.formatAllCharacterKeys(ctx, resetTime)
	}

	// Query for specific character name
	return c.formatCharacterKeys(ctx, args[0], resetTime)
}

func (c *DefaultDiscord) formatCharacterKeys(ctx context.Context, query string, since time.Time) (string, error) {
	allChars, err := c.store.ListCharacters(ctx)
	if err != nil {
		return "", err
	}

	// Check if query is in "name-realm" format
	var matchingChars []models.Character
	queryLower := strings.ToLower(query)

	// Try exact "name-realm" match first
	for _, char := range allChars {
		charKey := strings.ToLower(char.Name + "-" + char.Realm)
		if charKey == queryLower {
			matchingChars = append(matchingChars, char)
		}
	}

	// If no exact match, try name-only match
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

	// Check for ambiguous character name (same name on different servers)
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

	// Filter to only this realm
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

		// Filter to only this realm
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
	// Header with character name, realm, and count
	keyWord := "keys"
	if len(keys) == 1 {
		keyWord = "key"
	}
	sb.WriteString(fmt.Sprintf("**%s** (%s) — %d %s\n", char.Name, char.Realm, len(keys), keyWord))

	for _, key := range keys {
		completedAt := formatShortTime(key.CompletedAt)
		dungeonShort := shortenDungeonName(key.Dungeon)
		timing := formatTimingDiff(key.RunTimeMS, key.ParTimeMS)

		// Get WCL link if available
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
	if c.store == nil {
		return "", errors.New("database not configured")
	}

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

	// Sort by realm for deterministic output
	sort.Slice(matchingChars, func(i, j int) bool {
		return matchingChars[i].Realm < matchingChars[j].Realm
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Great Vault Progress** (Week of %s)\n", since.Format("Jan 2")))
	sb.WriteString("```ansi\n")

	for _, char := range matchingChars {
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

	// Sort characters by name for deterministic output
	sort.Slice(allChars, func(i, j int) bool {
		return allChars[i].Name < allChars[j].Name
	})

	if len(allChars) == 0 {
		return "No characters in database.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Great Vault Progress** (Week of %s)\n", since.Format("Jan 2")))
	sb.WriteString("```ansi\n")

	// Find max name length for alignment
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
			continue
		}

		var charKeys []models.CompletedKey
		for _, key := range keys {
			if strings.EqualFold(key.Realm, char.Realm) && strings.EqualFold(key.Region, char.Region) {
				charKeys = append(charKeys, key)
			}
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

	// Pad name for alignment
	paddedName := name + strings.Repeat(" ", maxNameLen-len(name))

	sb.WriteString(fmt.Sprintf("%s: %d keys %s %s %s\n",
		paddedName, keyCount, vault1, vault2, vault3))
}

// sortKeysByLevel sorts keys by KeyLevel descending (highest first)
func sortKeysByLevel(keys []models.CompletedKey) {
	for i := 0; i < len(keys)-1; i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j].KeyLevel > keys[i].KeyLevel {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
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
!keys <name>    - Show keys for a character
!keys all       - Show all keys completed this week
!report         - Show Great Vault progress for all characters
!report <name>  - Show Great Vault progress for a character
!help           - Show this help message
` + "```" + `
**Admin Commands** (requires bot-admin role):
` + "```" + `
!char sync <name> <realm>  - Sync character from RaiderIO
!char purge <name> <realm> - Remove character from database
` + "```" + `
*Use realm slugs (e.g., area-52, burning-legion). Region defaults to US.*
*For ambiguous names, use name-realm format (e.g., askr-mal-ganis)*`
}

// cmdChar handles character management commands (admin only)
func (c *DefaultDiscord) cmdChar(ctx context.Context, s *discordgo.Session, m *discordgo.MessageCreate, args []string) (string, error) {
	// Check for admin role
	if !c.hasAdminRole(s, m) {
		return "This command requires the `bot-admin` role.", nil
	}

	if len(args) < 1 {
		return "Usage: `!char sync <name> <realm>` or `!char purge <name> <realm>`", nil
	}

	subCmd := strings.ToLower(args[0])
	subArgs := args[1:]

	switch subCmd {
	case "sync":
		return c.cmdCharSync(ctx, subArgs)
	case "purge":
		return c.cmdCharPurge(ctx, subArgs)
	default:
		return "Unknown subcommand. Use `sync` or `purge`.", nil
	}
}

// hasAdminRole checks if the message author has the bot-admin role
func (c *DefaultDiscord) hasAdminRole(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	if s == nil || m == nil || m.Member == nil {
		return false
	}

	roles, err := s.GuildRoles(m.GuildID)
	if err != nil {
		return false
	}

	var adminRoleID string
	for _, role := range roles {
		if strings.EqualFold(role.Name, adminRoleName) {
			adminRoleID = role.ID
			break
		}
	}

	if adminRoleID == "" {
		return false
	}

	for _, roleID := range m.Member.Roles {
		if roleID == adminRoleID {
			return true
		}
	}

	return false
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

	// Fetch keys from RaiderIO
	keys, err := c.raiderIO.FetchWeeklyRuns(ctx, char)
	if err != nil {
		return "", fmt.Errorf("failed to fetch from RaiderIO: %w", err)
	}

	// Upsert keys into database
	insertedCount := 0
	for _, key := range keys {
		if err := c.store.UpsertCompletedKey(ctx, key); err == nil {
			insertedCount++
		}
	}

	// Link WarcraftLogs if available
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

	// Verify character exists
	_, err := c.store.GetCharacter(ctx, name, realm, region)
	if err != nil {
		return fmt.Sprintf("Character **%s** (%s-%s) not found in database.", name, realm, region), nil
	}

	// Delete character and all associated data
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
	return t.In(pstLocation).Format("Mon 3:04pm")
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
