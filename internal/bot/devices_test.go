package bot

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fus1ond/vpn_bot/internal/remnawave"
)

func TestBuildDevicesMessage(t *testing.T) {
	// нет устройств
	require.Contains(t, buildDevicesMessage(nil), "нет подключённых устройств")

	// есть устройства
	devices := []remnawave.HwidDevice{
		{Hwid: "hw-a", Platform: "iOS", DeviceModel: "iPhone 14"},
	}
	msg := buildDevicesMessage(devices)
	require.Contains(t, msg, "Подключено устройств: 1")
}

func TestDeviceByIndex(t *testing.T) {
	devices := []remnawave.HwidDevice{
		{Hwid: "hw-a"}, {Hwid: "hw-b"},
	}
	d, ok := deviceByIndex(devices, "1")
	require.True(t, ok)
	require.Equal(t, "hw-b", d.Hwid)

	_, ok = deviceByIndex(devices, "5")
	require.False(t, ok)

	_, ok = deviceByIndex(devices, "abc")
	require.False(t, ok)
}
