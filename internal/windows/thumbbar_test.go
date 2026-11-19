//go:build windows && (amd64 || arm64)

package windows

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"testing"
	"unsafe"

	"github.com/egoist/mygo/internal/platform"
)

// TestThumbButtonLayout asserts the thumbButton the shell reads as C laid
// THUMBBUTTON out (ShObjIdl_core.h): the icon sits 8-aligned between the
// bitmap and the tooltip.
func TestThumbButtonLayout(t *testing.T) {
	var b thumbButton
	if got, want := unsafe.Offsetof(b.icon), uintptr(16); got != want {
		t.Errorf("the icon lies at %d, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(b.tip), uintptr(24); got != want {
		t.Errorf("the tooltip lies at %d, want %d", got, want)
	}
	if got, want := unsafe.Sizeof(b), uintptr(552); got != want {
		t.Errorf("the button is %d bytes, want %d", got, want)
	}
}

// TestThumbButtonTipAndFlags checks tooltip truncation and flag updates.
func TestThumbButtonTipAndFlags(t *testing.T) {
	var b thumbButton
	b.setTip(strings.Repeat("x", 300))
	if b.tip[258] != 'x' || b.tip[259] != 0 {
		t.Error("a long tooltip is not cut to 259 runes and a zero")
	}
	b.setTip("")
	if b.tip[0] != 0 {
		t.Error("an empty tooltip is not empty")
	}
	b.setFlags(true, true)
	if b.flags != thbfDisabled|thbfHidden {
		t.Errorf("flags = %#x, want THBF_DISABLED and THBF_HIDDEN", b.flags)
	}
	// Reusing the same button clears the old flag bits.
	b.setFlags(false, true)
	if b.flags != thbfHidden {
		t.Errorf("flags = %#x, want THBF_HIDDEN", b.flags)
	}
	b.setFlags(false, false)
	if b.flags != 0 { // THBF_ENABLED
		t.Errorf("flags = %#x, want THBF_ENABLED", b.flags)
	}
}

// TestThumbBarState checks PNG validation with skipTaskbar, nil updates
// keeping icons, replacements, and atomic rollback on a bad PNG.
func TestThumbBarState(t *testing.T) {
	makePNG := func(c color.RGBA) []byte {
		var buf bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, 32, 32))
		draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	red := makePNG(color.RGBA{200, 40, 40, 255})
	blue := makePNG(color.RGBA{40, 40, 200, 255})
	green := makePNG(color.RGBA{40, 200, 40, 255})

	w := &window{b: &Backend{taskbarFailed: true}, skipTaskbar: true}

	// Even with skipTaskbar, a bad first PNG must fail and leave defined
	// false.
	if err := w.SetThumbBar([]platform.ThumbButton{{ID: 0, Icon: []byte("bad")}}); err == nil {
		t.Fatal("SetThumbBar accepted invalid PNG while skipTaskbar is true")
	}
	if w.thumb.defined {
		t.Fatal("defined should stay false after failed initial SetThumbBar")
	}

	if err := w.SetThumbBar([]platform.ThumbButton{
		{ID: 0, Icon: red, ToolTip: "Play"},
		{ID: 1, Icon: blue, ToolTip: "Next"},
	}); err != nil {
		t.Fatalf("initial SetThumbBar: %v", err)
	}

	// Update button 0 with no icon, replacing only button 1's icon.
	if err := w.SetThumbBar([]platform.ThumbButton{
		{ID: 0, ToolTip: "Play!"},
		{ID: 1, Icon: green, ToolTip: "Skip", Disabled: true},
	}); err != nil {
		t.Fatalf("update SetThumbBar: %v", err)
	}
	if !bytes.Equal(w.thumb.buttons[0].Icon, red) {
		t.Fatal("button 0 lost its saved PNG after nil-icon update")
	}
	if !bytes.Equal(w.thumb.buttons[1].Icon, green) {
		t.Fatal("button 1 did not save replacement PNG")
	}
	// The rebuild path (TaskbarButtonCreated / SetSkipTaskbar(false)) builds
	// every HICON from the cached PNGs.
	icons, err := w.makeThumbIcons(w.thumb.buttons, true)
	if err != nil {
		t.Fatalf("rebuild makeThumbIcons: %v", err)
	}
	destroyIcons(icons)

	// A bad replacement PNG fails without mutating the cached buttons.
	if err := w.SetThumbBar([]platform.ThumbButton{
		{ID: 0, Icon: []byte("corrupt"), ToolTip: "Broken"},
		{ID: 1, ToolTip: "Skip"},
	}); err == nil {
		t.Fatal("update SetThumbBar accepted invalid replacement PNG")
	}
	if w.thumb.buttons[0].ToolTip != "Play!" || !bytes.Equal(w.thumb.buttons[0].Icon, red) {
		t.Fatalf("failed update mutated cached button 0: %+v", w.thumb.buttons[0])
	}
}
