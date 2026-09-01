package rpc

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"github.com/iamxvbaba/td/tg"

	ioscompat "telesrv/internal/compat/ios"
	"telesrv/internal/updatecdn"
)

func (r *Router) onHelpGetAppUpdate(ctx context.Context, source string) (tg.HelpAppUpdateClass, error) {
	if _, _, err := r.currentUserID(ctx); err != nil {
		return nil, internalErr()
	}
	if r.deps.AppUpdates == nil {
		return ioscompat.NoAppUpdate(), nil
	}
	info, ok := ClientInfoFrom(ctx)
	if !ok {
		return ioscompat.NoAppUpdate(), nil
	}
	platform := updatePlatform(info.ClientType())
	if platform == "" {
		return ioscompat.NoAppUpdate(), nil
	}
	langCode := strings.TrimSpace(info.LangCode)
	if langCode == "" {
		langCode = strings.TrimSpace(info.SystemLangCode)
	}
	resolved, err := r.deps.AppUpdates.Resolve(ctx, updatecdn.ResolveRequest{
		Platform: platform,
		Channel:  updateChannel(info),
		Version:  info.AppVersion,
		Source:   boundedUpdateSource(source),
		LangCode: langCode,
	})
	if err != nil {
		// Update discovery is advisory. Returning a bounded no-update response
		// avoids turning a temporary CDN outage into a client-visible RPC 500.
		r.log.Warn("application update resolve failed",
			zap.String("platform", platform),
			zap.String("app_version", info.AppVersion),
			zap.Error(err))
		return ioscompat.NoAppUpdate(), nil
	}
	if resolved == nil {
		return ioscompat.NoAppUpdate(), nil
	}
	result := &tg.HelpAppUpdate{
		ID:       resolved.ID,
		Version:  resolved.Version,
		Text:     resolved.Text,
		Entities: []tg.MessageEntityClass{},
	}
	result.SetCanNotSkip(resolved.CanNotSkip)
	if resolved.URL != "" {
		result.SetURL(resolved.URL)
	}
	return result, nil
}

func updateChannel(info ClientInfo) string {
	version := strings.ToLower(info.AppVersion)
	switch {
	case strings.Contains(version, "alpha"):
		return "alpha"
	case strings.Contains(version, "beta"):
		return "beta"
	default:
		return "stable"
	}
}

func boundedUpdateSource(source string) string {
	source = strings.TrimSpace(source)
	if len(source) > 256 {
		return source[:256]
	}
	return source
}

func updatePlatform(clientType ClientType) string {
	switch clientType {
	case ClientTypeAndroid:
		return "android"
	case ClientTypeIOS:
		return "ios"
	case ClientTypeMacOS:
		return "macos"
	case ClientTypeTDesktop:
		return "tdesktop"
	default:
		return ""
	}
}
