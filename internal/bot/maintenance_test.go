package bot

import "testing"

func TestMaintenanceModeHelpers(t *testing.T) {
	b := &Bot{}

	if b.isMaintenanceMode() {
		t.Fatal("режим обслуживания должен быть выключен по умолчанию")
	}

	b.setMaintenanceMode(true)
	if !b.isMaintenanceMode() {
		t.Fatal("режим обслуживания должен включаться через helper")
	}

	if enabled := b.toggleMaintenanceMode(); enabled {
		t.Fatal("toggle должен выключить режим обслуживания")
	}

	if b.isMaintenanceMode() {
		t.Fatal("режим обслуживания должен быть выключен после toggle")
	}
}
