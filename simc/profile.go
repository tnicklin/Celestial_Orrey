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
// this map are ignored by (shirt, tabard, ranged, etc.).
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

// trackInCommentRE finds an ilvl + track pair in a comment line. The TWW
// addon used "# Name (276 Hero 7/8)" — both pieces present.
var trackInCommentRE = regexp.MustCompile(`\b(\d{2,3})\s+(Hero|Myth|Champion|Veteran|Adventurer|Explorer)\b`)

// ilvlInCommentRE finds a bare ilvl in a comment line. The Midnight addon
// shortened comments to "# Name (276)" — no track keyword. We pair this
// with inferTrackFromIlvl as the fallback.
var ilvlInCommentRE = regexp.MustCompile(`[(\[](\d{2,3})[)\]]`)

// commentNameRE pulls the item name from a comment line. We accept any
// number of leading '#' (some addon versions emit '##') and take everything
// up to the first '(' or '[' as the name. Requires a bracket so the lazy
// match doesn't degenerate to a single character.
var commentNameRE = regexp.MustCompile(`^#+\s*(.+?)\s*[(\[]`)

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

// classDeclRE matches a class declaration line (e.g. priest="Askrlol").
var classDeclRE = regexp.MustCompile(`^(deathknight|demonhunter|druid|evoker|hunter|mage|monk|paladin|priest|rogue|shaman|warlock|warrior)="([^"]+)"`)

// kvLineRE matches simple `key=value` header lines.
var kvLineRE = regexp.MustCompile(`^([a-z_]+)=([^\n]+)$`)

// CharacterName returns the character name from the class declaration line.
func (p Profile) CharacterName() string {
	for _, line := range p.Header {
		if m := classDeclRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			return m[2]
		}
	}
	return ""
}

// ClassName returns the class identifier (e.g. "priest") from the class
// declaration line.
func (p Profile) ClassName() string {
	for _, line := range p.Header {
		if m := classDeclRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			return m[1]
		}
	}
	return ""
}

// Realm returns the value of `server=`.
func (p Profile) Realm() string { return p.headerValue("server") }

// Spec returns the value of `spec=`.
func (p Profile) Spec() string { return p.headerValue("spec") }

// Region returns the value of `region=`.
func (p Profile) Region() string { return p.headerValue("region") }

func (p Profile) headerValue(key string) string {
	for _, line := range p.Header {
		t := strings.TrimSpace(line)
		if m := kvLineRE.FindStringSubmatch(t); m != nil && m[1] == key {
			return strings.TrimSpace(m[2])
		}
	}
	return ""
}

// CandidatesBySlot returns sim-eligible items (hero + myth track) grouped
// by slot. Equipped items are included as candidates. Duplicates by
// fingerprint are removed. Slots the user does NOT have equipped are
// dropped entirely — this avoids generating combinations like
// "2H staff + off-hand" which simc rejects.
func (p Profile) CandidatesBySlot() map[Slot][]Item {
	equippedSlots := make(map[Slot]bool)
	for _, it := range p.Items {
		if it.Equipped {
			equippedSlots[it.Slot] = true
		}
	}

	out := make(map[Slot][]Item)
	seen := make(map[string]map[string]bool)
	for _, it := range p.Items {
		if !equippedSlots[it.Slot] {
			continue
		}
		if !it.Track.IsEligible() {
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
		// Fallback: if the comment didn't yield a track but we have an ilvl,
		// infer the track from the ilvl bands. This catches addon variants
		// that omit the track keyword from their comment.
		if it.Track == TrackUnknown && it.OriginalIlvl > 0 {
			it.Track = inferTrackFromIlvl(it.OriginalIlvl)
		}
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
		case "gem_id", "gems":
			it.GemIDs = v
		case "enchant_id", "enchant":
			it.EnchantID = v
		case "ilevel":
			// Skipped — we re-emit our own based on EffectiveIlvl.
		default:
			it.Extras = append(it.Extras, token)
		}
	}
	return it
}

// applyCommentMeta extracts ilvl + track from the comment line just above
// the item. Tries the explicit "ilvl + Track" format first (TWW addon),
// falls back to "(ilvl)" alone (Midnight addon) where track is inferred
// from ilvl bands by the caller.
func applyCommentMeta(it *Item, comment string) {
	if comment == "" {
		return
	}
	if m := trackInCommentRE.FindStringSubmatch(comment); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			it.OriginalIlvl = n
		}
		it.Track = parseTrack(m[2])
	} else if m := ilvlInCommentRE.FindStringSubmatch(comment); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			it.OriginalIlvl = n
		}
	}
	if m := commentNameRE.FindStringSubmatch(comment); m != nil {
		it.Name = strings.TrimSpace(m[1])
	}
}

// inferTrackFromIlvl returns a Track based on a Midnight S1 ilvl threshold.
// Used as a fallback when the addon comment is missing or the regex didn't
// pick up a track keyword. Boundaries are deliberately conservative — items
// below 252 are excluded from anyway.
func inferTrackFromIlvl(ilvl int) Track {
	switch {
	case ilvl >= 278:
		return TrackMyth
	case ilvl >= 252:
		return TrackHero
	case ilvl >= 226:
		return TrackChampion
	case ilvl >= 213:
		return TrackVeteran
	case ilvl >= 200:
		return TrackAdventurer
	}
	return TrackUnknown
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
