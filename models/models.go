package models

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

type Character struct {
	Name     string  `json:"name" yaml:"name"`
	Realm    string  `json:"realm" yaml:"realm"`
	Region   string  `json:"region" yaml:"region"`
	RIOScore float64 `json:"rio_score" yaml:"rio_score"`
}

func (c Character) Key() string {
	return strings.ToLower(strings.TrimSpace(c.Region)) + "|" +
		strings.ToLower(strings.TrimSpace(c.Realm)) + "|" +
		strings.ToLower(strings.TrimSpace(c.Name))
}

type CompletedKey struct {
	KeyID       int64  `json:"key_id" yaml:"key_id"`
	Character   string `json:"character" yaml:"character"`
	Region      string `json:"region" yaml:"region"`
	Realm       string `json:"realm" yaml:"realm"`
	Dungeon     string `json:"dungeon" yaml:"dungeon"`
	KeyLevel    int    `json:"key_lvl" yaml:"key_lvl"`
	RunTimeMS   int64  `json:"run_time_ms" yaml:"run_time_ms"`
	ParTimeMS   int64  `json:"par_time_ms" yaml:"par_time_ms"`
	CompletedAt string `json:"completed_at" yaml:"completed_at"`
	Source      string `json:"source" yaml:"source"`
}

func (k CompletedKey) KeyIDOrSynthetic() string {
	if k.KeyID > 0 {
		return fmt.Sprintf("key:%d", k.KeyID)
	}
	return "syn:" + k.SyntheticKey()
}

func (k CompletedKey) SyntheticKey() string {
	parts := []string{
		normalize(k.Region),
		normalize(k.Realm),
		normalize(k.Character),
		normalize(k.Dungeon),
		fmt.Sprintf("%d", k.KeyLevel),
		fmt.Sprintf("%d", k.RunTimeMS),
		fmt.Sprintf("%d", k.ParTimeMS),
		normalize(k.CompletedAt),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

func normalize(in string) string {
	return strings.ToLower(strings.TrimSpace(in))
}
