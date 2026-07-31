package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// reportBindings prints what the re-site means for the OAuth clients and the
// developer API keys, and CHANGES NOTHING. Credentials and bindings are the
// window operator's decision; a tool that quietly edited them would be making
// an authorization change as a side effect of a data migration.
//
// It reads the INFRA database (oauth_clients / developer_api_keys), which is a
// different pool from the catalog one — hence the separate --infra-dsn.
func reportBindings(ctx context.Context, db *gorm.DB) error {
	var clients []struct {
		ID           string `gorm:"column:id"`
		Name         string `gorm:"column:name"`
		CatalogSite  string `gorm:"column:catalog_site"`
		ImageSiteKey string `gorm:"column:image_site_key"`
	}
	if err := db.WithContext(ctx).Raw(
		`SELECT id, name, coalesce(catalog_site, '') AS catalog_site,
		        coalesce(image_site_key, '') AS image_site_key
		 FROM oauth_clients
		 WHERE coalesce(catalog_site, '') <> '' OR coalesce(image_site_key, '') <> ''
		 ORDER BY name`).Scan(&clients).Error; err != nil {
		return err
	}

	fmt.Println("\n── OAuth clients carrying a catalog or image binding ──")
	fmt.Printf("  %-34s %-18s %-18s %s\n", "client", "catalog_site", "image_site_key", "verdict")
	for _, c := range clients {
		fmt.Printf("  %-34s %-18s %-18s %s\n",
			truncate(c.Name, 34), dash(c.CatalogSite), dash(c.ImageSiteKey), clientVerdict(c.CatalogSite, c.ImageSiteKey))
	}

	var keys []struct {
		Name      string  `gorm:"column:name"`
		ClientID  string  `gorm:"column:client_id"`
		Scopes    string  `gorm:"column:scopes"`
		RevokedAt *string `gorm:"column:revoked_at"`
	}
	if err := db.WithContext(ctx).Raw(
		`SELECT name, client_id, scopes::text AS scopes, revoked_at::text AS revoked_at
		 FROM developer_api_keys ORDER BY id`).Scan(&keys).Error; err != nil {
		return err
	}

	fmt.Println("\n── developer API keys holding galgame:* scopes ──")
	for _, k := range keys {
		if k.RevokedAt != nil {
			continue
		}
		var scopes []string
		_ = json.Unmarshal([]byte(k.Scopes), &scopes)
		var dead []string
		for _, s := range scopes {
			if strings.HasPrefix(s, "galgame:") {
				dead = append(dead, s)
			}
		}
		if len(dead) == 0 {
			continue
		}
		fmt.Printf("  %-32s dead after P5: %s\n", truncate(k.Name, 32), strings.Join(dead, " "))
	}
	fmt.Println(`
  Verdict (REPORT ONLY — this tool changes nothing):
    · galgame:* scopes stop resolving the moment P5 removes the galgame HTTP
      faces. They then grant nothing, so they are noise rather than exposure,
      and stripping them is a tidy-up AFTER the window, not a prerequisite of
      it. Doing it before would break the wiki faces while they are still live.
    · none of this is a reason to rotate a key. The outstanding rotation item
      is the 2026-07-23 plaintext exposure, which is a separate decision and
      unaffected either way.
    · image_site_key='galgame_wiki' on galgame-wiki-admin is NEVER touched:
      the image bytes keep that site scope forever (the standing red line), and
      the refping wiki lane retires as CODE, not as data.`)
	return nil
}

// clientVerdict states what the re-site implies for one client binding.
func clientVerdict(catalogSite, imageSiteKey string) string {
	switch {
	case catalogSite == toSite:
		return "already aligned — inherits the re-sited claims, no change"
	case catalogSite == fromSite:
		return "STOP: a client still bound to the retiring site"
	case imageSiteKey == fromSite:
		return "image scope only — never touched (bytes keep this site forever)"
	case catalogSite == "":
		return "no catalog binding — cannot claim; unaffected by the re-site"
	default:
		return "other tenant — unaffected"
	}
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
