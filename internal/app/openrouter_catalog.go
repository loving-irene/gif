package app

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"strings"
)

const defaultImageModel = "openai/gpt-image-2.6-sunburst"

// Upgrade only the old default, once. An administrator can still deliberately
// select the historical model afterwards; custom model/provider choices survive.
func (a *App) migrateDefaultImageModel() error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var marker string
	err = tx.QueryRow("SELECT value FROM settings WHERE key='image_default_26'").Scan(&marker)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	var raw string
	if err = tx.QueryRow("SELECT value FROM settings WHERE key='config'").Scan(&raw); err != nil {
		return err
	}
	var cfg map[string]any
	if err = json.Unmarshal([]byte(raw), &cfg); err != nil {
		return err
	}
	base, _ := cfg["apiBase"].(string)
	model, _ := cfg["model"].(string)
	if (strings.TrimRight(strings.TrimSpace(base), "/") == openRouterAPIBase || base == "") &&
		(model == "openai/gpt-image-2.5-sunburst" || model == "gpt-image-2.5-sunburst" || model == "") {
		cfg["model"] = defaultImageModel
		b, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE settings SET value=? WHERE key='config'", string(b)); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES('image_default_26','1')"); err != nil {
		return err
	}
	return tx.Commit()
}

// Snapshot of the public image-model endpoint, checked on 2026-11-06.
// Keep the parameter contract alongside the IDs so new models do not inherit
// unsupported options from an older member of their family.
//
//go:embed openrouter_models.json
var openRouterModelJSON []byte

type imageParameter struct {
	Type   string   `json:"type"`
	Values []string `json:"values"`
	Min    int      `json:"min"`
	Max    int      `json:"max"`
}

type routerModel struct {
	ID         string                    `json:"id"`
	Name       string                    `json:"name"`
	Parameters map[string]imageParameter `json:"supported_parameters"`
}

var routerModels = func() []routerModel {
	var models []routerModel
	if err := json.Unmarshal(openRouterModelJSON, &models); err != nil {
		panic(err)
	}
	return models
}()

func latestImageModels() []ImageModel {
	var out []ImageModel
	counts := map[string]int{}
	for _, m := range routerModels {
		// Motion uses both a draft and a selfie; SVG cannot be sliced by Canvas/Go.
		if m.Parameters["input_references"].Max < 2 {
			continue
		}
		formats := m.Parameters["output_format"].Values
		if len(formats) == 1 && formats[0] == "svg" {
			continue
		}
		vendor, name, ok := strings.Cut(m.Name, ": ")
		if !ok {
			vendor, name = strings.SplitN(m.ID, "/", 2)[0], m.Name
		}
		counts[vendor]++
		out = append(out, ImageModel{ID: m.ID, Name: name, Vendor: vendor, Family: modelFamilyPrefix(m.ID), Latest: counts[vendor] <= 2, Supports: true})
	}
	return out
}

func supportsDirectGIF(cfg Settings) bool {
	if strings.TrimRight(cfg.APIBase, "/") != openRouterAPIBase {
		return false
	}
	for _, m := range routerModels {
		if m.ID == cfg.Model {
			return contains(m.Parameters["output_format"].Values, "gif")
		}
	}
	return false
}

func motionOutputFormat(cfg Settings, kind string) string {
	if kind == "motion" && supportsDirectGIF(cfg) {
		return "gif"
	}
	return ""
}

func constrainImagePayload(model string, payload map[string]any) map[string]any {
	for _, m := range routerModels {
		if m.ID != model {
			continue
		}
		for key, value := range payload {
			if key == "model" || key == "prompt" {
				continue
			}
			p, ok := m.Parameters[key]
			if !ok {
				delete(payload, key)
				continue
			}
			if s, ok := value.(string); ok && len(p.Values) > 0 && !contains(p.Values, s) {
				if key == "output_format" {
					payload[key] = p.Values[0]
				} else {
					delete(payload, key)
				}
			}
		}
		break
	}
	return payload
}
