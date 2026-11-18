package ui

import "testing"

// benchFrame measures one animation frame on a large terminal: the motion
// and one render.
func benchFrame(b *testing.B, a Animation) {
	b.Helper()
	const w, h = 200, 50
	a.View(w, h)
	b.ReportAllocs()
	for b.Loop() {
		advance(a, w, h)
		_ = a.View(w, h)
	}
}

func BenchmarkCloudAnimationFrame(b *testing.B) { benchFrame(b, NewCloudAnimation(1)) }
func BenchmarkStormAnimationFrame(b *testing.B) { benchFrame(b, NewStormAnimation(1)) }

// advance moves the animation by one frame.
func advance(a Animation, w, h int) { a.Update(w, h) }
