//go:build !embedpanel

package httpapi

import "io/fs"

// Без тега embedpanel панель берётся из каталога: так её правят на
// объекте, не пересобирая центр.
func embeddedPanel() (fs.FS, bool) { return nil, false }
