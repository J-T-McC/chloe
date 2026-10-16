package core

// BISharedReads counts the chart reads served by a shared read of their source.
func (m *Manager) BISharedReads() int64 { return m.biBatch.shared.Load() }
