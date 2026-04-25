package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log/slog"
	"os"
	"time"

	"fyne.io/systray"
)

type subscribeConfig struct {
	socketPath string
	user       string
}

func runTray(ctx context.Context, cancel context.CancelFunc, logger *slog.Logger, cfg subscribeConfig) {
	onReady := func() {
		idleIcon := systemIcon()
		activeIcon := withActivityDot(idleIcon)
		systray.SetIcon(idleIcon)
		systray.SetTooltip("nodemanager agent")

		statusItem := systray.AddMenuItem("Connecting…", "")
		statusItem.Disable()
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit", "Quit the agent")

		go func() {
			select {
			case <-quit.ClickedCh:
				cancel()
			case <-ctx.Done():
			}
			systray.Quit()
		}()

		go func() {
			var revertTimer *time.Timer
			onStatus := func(connected bool) {
				if connected {
					statusItem.SetTitle("● Connected")
				} else {
					statusItem.SetTitle("○ Disconnected")
				}
			}
			onActivity := func() {
				systray.SetIcon(activeIcon)
				if revertTimer != nil {
					revertTimer.Stop()
				}
				revertTimer = time.AfterFunc(2*time.Second, func() {
					systray.SetIcon(idleIcon)
				})
			}
			if err := subscribe(ctx, logger, cfg.socketPath, cfg.user, onStatus, onActivity); err != nil {
				logger.Error("agent exited with error", "err", err)
			}
			cancel()
			systray.Quit()
		}()
	}

	systray.Run(onReady, func() {})
}

// systemIcon returns bytes for the tray icon. Tries well-known system icon
// paths first so the icon matches the desktop theme. Falls back to a generated
// placeholder when none are found.
//
// To use a custom icon instead, replace this function body with:
//
//	//go:embed icon.png
//	var iconBytes []byte
//	func systemIcon() []byte { return iconBytes }
func systemIcon() []byte {
	const name = "preferences-system"
	for _, size := range []int{22, 32, 48} {
		s := fmt.Sprintf("%dx%d", size, size)
		for _, path := range []string{
			fmt.Sprintf("/usr/share/icons/hicolor/%s/apps/%s.png", s, name),
			fmt.Sprintf("/usr/share/icons/Adwaita/%s/legacy/%s.png", s, name),
			fmt.Sprintf("/usr/share/icons/Papirus/%s/apps/%s.png", s, name),
			fmt.Sprintf("/usr/share/icons/Papirus/%s/categories/%s.png", s, name),
		} {
			if data, err := os.ReadFile(path); err == nil {
				return data
			}
		}
	}
	return generatedIcon()
}

// generatedIcon produces a minimal 22x22 blue square PNG used as a last-resort
// fallback when no system icon is found.
func generatedIcon() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 22, 22))
	c := color.RGBA{R: 50, G: 130, B: 220, A: 255}
	for y := range 22 {
		for x := range 22 {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// withActivityDot returns a copy of the icon PNG with a small amber dot drawn
// in the bottom-right corner to indicate reconciliation activity.
func withActivityDot(iconData []byte) []byte {
	src, err := png.Decode(bytes.NewReader(iconData))
	if err != nil {
		return iconData
	}

	b := src.Bounds()
	img := image.NewRGBA(b)
	draw.Draw(img, b, src, b.Min, draw.Src)

	// Draw a filled circle in the bottom-right corner.
	dotColor := color.RGBA{R: 245, G: 166, B: 35, A: 255} // amber
	radius := max(b.Dx()/5, 3)
	cx := b.Max.X - radius - 1
	cy := b.Max.Y - radius - 1
	for y := cy - radius; y <= cy+radius; y++ {
		for x := cx - radius; x <= cx+radius; x++ {
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= radius*radius {
				img.Set(x, y, dotColor)
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
