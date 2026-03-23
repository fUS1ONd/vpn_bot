package bot

// isMaintenanceMode возвращает текущее состояние режима обслуживания.
func (b *Bot) isMaintenanceMode() bool {
	return b.maintenanceMode.Load()
}

// setMaintenanceMode явно устанавливает режим обслуживания.
func (b *Bot) setMaintenanceMode(enabled bool) {
	b.maintenanceMode.Store(enabled)
}

// toggleMaintenanceMode переключает режим обслуживания и возвращает новое состояние.
func (b *Bot) toggleMaintenanceMode() bool {
	for {
		current := b.maintenanceMode.Load()
		next := !current
		if b.maintenanceMode.CompareAndSwap(current, next) {
			return next
		}
	}
}
