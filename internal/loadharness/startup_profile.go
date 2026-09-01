package loadharness

import (
	"fmt"
	"strings"
	"time"

	"github.com/iamxvbaba/td/telegram"
)

const (
	StartupProfileTDesktopReturningV1 = "tdesktop-cold-returning-v1"
	StartupProfileTDLibReturningV1    = "tdlib-returning-v1"
)

type dialogPaginationProfile struct {
	FirstLimit      int
	SubsequentLimit int
}

type startupWorkloadProfile struct {
	Name                     string
	GetStateBeforeDifference bool
	AccountDifference        bool
	Dialogs                  dialogPaginationProfile
	ForceChannelDifference   bool
}

func resolveStartupProfile(name string) (startupWorkloadProfile, error) {
	switch strings.TrimSpace(name) {
	case "", StartupProfileTDesktopReturningV1:
		return startupWorkloadProfile{
			Name: StartupProfileTDesktopReturningV1, GetStateBeforeDifference: true,
			Dialogs:                dialogPaginationProfile{FirstLimit: 20, SubsequentLimit: 500},
			ForceChannelDifference: true,
		}, nil
	case StartupProfileTDLibReturningV1:
		return startupWorkloadProfile{
			Name:                   StartupProfileTDLibReturningV1,
			AccountDifference:      true,
			Dialogs:                dialogPaginationProfile{FirstLimit: 100, SubsequentLimit: 100},
			ForceChannelDifference: true,
		}, nil
	default:
		return startupWorkloadProfile{}, fmt.Errorf("unknown startup profile %q", name)
	}
}

func (p dialogPaginationProfile) limit(page int) int {
	if page == 0 {
		return p.FirstLimit
	}
	return p.SubsequentLimit
}

func (p startupWorkloadProfile) device() telegram.DeviceConfig {
	if p.Name == StartupProfileTDLibReturningV1 {
		return telegram.DeviceConfig{
			DeviceModel: "telesrv-flutter TDLib", SystemVersion: "Android SDK 36", AppVersion: "load-profile-v1",
			SystemLangCode: "en-US", LangPack: "android", LangCode: "en",
			Params: telegram.TimezoneParams(time.Local),
		}
	}
	return telegram.DeviceTDesktopWindows()
}

var snapshotPaginationProfile = dialogPaginationProfile{FirstLimit: 100, SubsequentLimit: 100}
