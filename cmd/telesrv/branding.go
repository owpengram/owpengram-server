package main

import (
	"strings"

	"telesrv/internal/branding"
)

// brandedConfig derives a full branding.Config from the operator's custom
// server identity name. Every *Name field in branding.DefaultConfig() is
// either exactly "OwpenGram" or "OwpenGram <something>" (Premium, Stars,
// Desktop, Android, ...); replacing that shared prefix wherever it appears
// keeps the whole branding set -- not just ProductName -- consistent with
// the one name the operator actually configured in Server Settings ->
// Server identity. name must already be non-empty and trimmed.
func brandedConfig(name string) branding.Config {
	def := branding.DefaultConfig()
	renamed := func(s string) string { return strings.Replace(s, def.ProductName, name, 1) }
	cfg := def
	cfg.ProductName = name
	cfg.DesktopAppName = renamed(def.DesktopAppName)
	cfg.AndroidAppName = renamed(def.AndroidAppName)
	cfg.IOSAppName = renamed(def.IOSAppName)
	cfg.MacOSAppName = renamed(def.MacOSAppName)
	cfg.WebAAppName = renamed(def.WebAAppName)
	cfg.WebKAppName = renamed(def.WebKAppName)
	cfg.PremiumName = renamed(def.PremiumName)
	cfg.StarsName = renamed(def.StarsName)
	return cfg
}
