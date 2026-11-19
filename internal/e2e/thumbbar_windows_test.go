//go:build windows && (amd64 || arm64)

package e2e

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/internal/wait"
	win "github.com/egoist/mygo/internal/windows"
)

// TestThumbBar sets a thumbnail toolbar on a real window, updates it, and
// runs a button's Click the way the shell reports one, with the
// WM_COMMAND it sends.
func TestThumbBar(t *testing.T) {
	w := newWindow(t, mygo.WindowOptions{Title: "ThumbBar", Width: 300, Height: 200})
	clicks := make(chan int, 8)
	click := func(i int) func(*mygo.ThumbButton, *mygo.Window) {
		return func(*mygo.ThumbButton, *mygo.Window) { clicks <- i }
	}
	icon := func(c color.RGBA) []byte {
		var buf bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, 64, 64))
		draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	// A button with a bad PNG fails, leaving the toolbar unset: the next
	// call defines it.
	err := w.SetThumbBar([]mygo.ThumbButton{{Icon: []byte("not a png"), ToolTip: "Bad"}})
	if err == nil {
		t.Fatal("SetThumbBar accepted a bad image")
	}
	if err := w.SetThumbBar([]mygo.ThumbButton{
		{Icon: icon(color.RGBA{200, 40, 40, 255}), ToolTip: "First", Click: click(0)},
		{Icon: icon(color.RGBA{40, 40, 200, 255}), ToolTip: "Second", Click: click(1)},
	}); err != nil {
		t.Fatalf("SetThumbBar: %v", err)
	}
	w.Show()
	eventually(t, "the window shown", w.IsVisible)

	// A later call updates tooltips, state, icons and Click functions; nil
	// keeps the existing icon, and passing a new PNG replaces it.
	firstIcon := icon(color.RGBA{200, 40, 40, 255})
	if err := w.SetThumbBar([]mygo.ThumbButton{
		{ToolTip: "First!", Click: click(10)},
		{Icon: icon(color.RGBA{90, 90, 90, 255}), Disabled: true, Click: click(11)},
	}); err != nil {
		t.Fatalf("SetThumbBar again: %v", err)
	}
	// Passing the original icon again, recreating the taskbar button and
	// updating progress all still work.
	if err := w.SetThumbBar([]mygo.ThumbButton{
		{Icon: firstIcon, ToolTip: "First!", Click: click(10)},
		{Disabled: true, Click: click(11)},
	}); err != nil {
		t.Fatalf("SetThumbBar with original icon again: %v", err)
	}
	w.SetSkipTaskbar(true)
	w.SetSkipTaskbar(false)
	w.SetProgressBar(mygo.ProgressBar{State: mygo.ProgressNormal, Value: 0.5})

	// The button count is fixed once set, and a bad replacement icon fails.
	if err := w.SetThumbBar([]mygo.ThumbButton{{ToolTip: "One"}}); err == nil {
		t.Error("SetThumbBar changed the number of buttons")
	}
	if err := w.SetThumbBar([]mygo.ThumbButton{
		{Icon: []byte("not a png"), ToolTip: "Bad"},
		{ToolTip: "Second"},
	}); err == nil {
		t.Error("SetThumbBar accepted a bad replacement icon")
	}

	// The shell reports a click with the button's id in a WM_COMMAND.
	win.TestThumbButtonClicked(w.NativeHandle(), 0)
	if i := wait.For(t, "the button's click", clicks); i != 10 {
		t.Errorf("the click ran Click of button %d, want 10", i)
	}
	// A button the toolbar does not have runs nothing: the shell keeps its
	// size in step with the core's. The click reaches the main thread after
	// the posted WM_COMMAND, so give it time to get there.
	win.TestThumbButtonClicked(w.NativeHandle(), 2)
	select {
	case i := <-clicks:
		t.Errorf("a click ran Click of button %d", i)
	case <-time.After(300 * time.Millisecond):
	}
}
