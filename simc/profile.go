package simc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// addonSlotMap maps each addon slot keyword to our Slot enum. Slots not in
// this map are ignored by BiB (shirt, tabard, ranged, etc.).
var addonSlotMap = map[string]Slot{
	"head":      SlotHead,
	"neck":      SlotNeck,
	"shoulders": SlotShoulders,
	"shoulder":  SlotShoulders,
	"back":      SlotBack,
	"chest":     SlotChest,
	"wrists":    SlotWrists,
	"wrist":     SlotWrists,
	"hands":     SlotHands,
	"waist":     SlotWaist,
	"legs":      SlotLegs,
	"feet":      SlotFeet,
	"finger1":   SlotFinger,
	"finger2":   SlotFinger,
	"trinket1":  SlotTrinket,
	"trinket2":  SlotTrinket,
	"main_hand": SlotMainHand,
	"off_hand":  SlotOffHand,
}

// itemLineRE matches a simc item line, optionally prefixed with a single '#'
// (bag items are commented). The slot key is captured.
var itemLineRE = regexp.MustCompile(`^#?\s*([a-z_0-9]+)=,(.*)$`)

// commentItemMetaRE captures the human-readable comment immediately preceding
// each item line, e.g. "# Resilient Loop of the Eternal (272 Hero 7/8)".
// We pull out the name, ilvl, and track.
var commentItemMetaRE = regexp.MustCompile(`^#\s*(.+?)\s*\((\d+)\s+([A-Za-z]+)`)

// Profile is the parsed addon dump. Header holds the character/talents/etc.
// lines that we replicate verbatim into every generated combination.
type Profile struct {
	Header   []string // every line above the first item line
	Footer   []string // every line after the last item line (e.g. trailing blanks)
	Items    []Item
	RawBytes []byte
}

// EquippedBySlot returns the equipped items grouped by slot. The order
// preserves the addon's emission (finger1 before finger2 etc.).
func (p Profile) EquippedBySlot() map[Slot][]Item {
	out := make(map[Slot][]Item)
	for _, it := range p.Items {
		if !it.Equipped {
			continue
		}
		out[it.Slot] = append(out[it.Slot], it)
	}
	return out
}

// CandidatesBySlot returns BiB-eligible items (hero + myth track) grouped
// by slot. Equipped items are included as candidates. Duplicates by
// fingerprint are removed.
func (p Profile) CandidatesBySlot() map[Slot][]Item {
	out := make(map[Slot][]Item)
	seen := make(map[string]map[string]bool)
	for _, it := range p.Items {
		if !it.Track.IsBiBEligible() {
			continue
		}
		if _, ok := seen[it.Slot.String()]; !ok {
			seen[it.Slot.String()] = make(map[string]bool)
		}
		fp := it.fingerprint()
		if seen[it.Slot.String()][fp] {
			continue
		}
		seen[it.Slot.String()][fp] = true
		out[it.Slot] = append(out[it.Slot], it)
	}
	return out
}

// ParseProfile parses the contents of a /simc addon dump.
func ParseProfile(b []byte) (*Profile, error) {
	if err := ValidateProfile(b); err != nil {
		return nil, err
	}
	p := &Profile{RawBytes: b}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var lastComment string
	headerDone := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		matches := itemLineRE.FindStringSubmatch(trimmed)
		if matches == nil {
			// Not an item line. Track standalone comments so the next item
			// can pick up its meta. Skip "###" section headers — those don't
			// describe individual items.
			if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "###") {
				lastComment = trimmed
			}
			if !headerDone {
				p.Header = append(p.Header, line)
			} else {
				p.Footer = append(p.Footer, line)
			}
			continue
		}
		slotKey := matches[1]
		slot, ok := addonSlotMap[slotKey]
		if !ok {
			// Unrecognized slot (shirt, tabard, ranged) — keep verbatim in footer
			// for transparency but don't treat as item.
			if !headerDone {
				p.Header = append(p.Header, line)
			} else {
				p.Footer = append(p.Footer, line)
			}
			continue
		}

		headerDone = true
		equipped := !strings.HasPrefix(trimmed, "#")
		fields := matches[2]

		it := parseItemFields(slot, slotKey, equipped, fields)
		applyCommentMeta(&it, lastComment)
		lastComment = ""
		p.Items = append(p.Items, it)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan profile: %w", err)
	}
	if len(p.Items) == 0 {
		return nil, errors.New("no item lines found in profile")
	}
	return p, nil
}

// parseItemFields parses a comma-separated `key=value` token list. We
// preserve any fields we don't explicitly handle in Item.Extras so that
// crafted stats, drop level, etc. survive a Render() round-trip.
func parseItemFields(slot Slot, slotKey string, equipped bool, fields string) Item {
	it := Item{Slot: slot, AddonSlotKey: slotKey, Equipped: equipped}
	for _, token := range strings.Split(fields, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		k, v, ok := strings.Cut(token, "=")
		if !ok {
			it.Extras = append(it.Extras, token)
			continue
		}
		switch strings.ToLower(k) {
		case "id":
			if n, err := strconv.Atoi(v); err == nil {
				it.ItemID = n
			}
		case "bonus_id":
			it.BonusIDs = v
		case "crafted_stats":
			it.CraftedStats = v
		case "context":
			it.Context = v
		case "gem_id", "gems", "enchant_id", "enchant", "ilevel":
			// Strip per spec: gems and enchants always, ilevel because we
			// re-emit our own.
		default:
			it.Extras = append(it.Extras, token)
		}
	}
	return it
}

// applyCommentMeta extracts ilvl + track from the comment line just above
// the item, e.g. "# Some Item Name (272 Hero 7/8)".
func applyCommentMeta(it *Item, comment string) {
	if comment == "" {
		return
	}
	m := commentItemMetaRE.FindStringSubmatch(comment)
	if m == nil {
		return
	}
	it.Name = strings.TrimSpace(m[1])
	if n, err := strconv.Atoi(m[2]); err == nil {
		it.OriginalIlvl = n
	}
	it.Track = parseTrack(m[3])
}

func parseTrack(s string) Track {
	switch strings.ToLower(s) {
	case "explorer":
		return TrackExplorer
	case "adventurer":
		return TrackAdventurer
	case "veteran":
		return TrackVeteran
	case "champion":
		return TrackChampion
	case "hero":
		return TrackHero
	case "myth":
		return TrackMyth
	}
	return TrackUnknown
}

// itoa is strconv.Itoa, exported here just to keep items.go independent of
// the strconv import.
func itoa(n int) string { return strconv.Itoa(n) }

// joinFields joins simc item-line fields. The first field is the leading
// `,id=` token (already starts with comma), subsequent fields are
// comma-prefixed.
func joinFields(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(parts[0])
	for _, p := range parts[1:] {
		sb.WriteString(",")
		sb.WriteString(p)
	}
	return sb.String()
}
