package ui

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// AnimationFrameMsg signals a new animation frame. Gen identifies the chain
// of ticks. A view that starts its animation again increases its generation
// and ignores the frames of older chains, so only one chain runs.
type AnimationFrameMsg struct {
	Gen int
}

// AnimationTick returns a command that schedules the next frame of chain gen
// at ~20 FPS.
func AnimationTick(gen int) tea.Cmd {
	return tea.Tick(time.Second/20, func(time.Time) tea.Msg {
		return AnimationFrameMsg{Gen: gen}
	})
}

// Animation is an idle animation that renders into a given area.
type Animation interface {
	// Update advances the animation by one frame in an area of the given
	// dimensions. All motion happens here, so the speed does not depend on
	// how often the screen is drawn.
	Update(width, height int)
	// View renders the current frame into a string of the given
	// dimensions. It does not move anything, and it draws a frame only once.
	View(width, height int) string
	// SpeedUp increases the animation speed.
	SpeedUp()
	// SlowDown decreases the animation speed.
	SlowDown()
}

// cloudBump is one circle in a cloud's silhouette.
type cloudBump struct {
	cx, cy float64 // center position relative to cloud origin
	r      float64 // radius in pixels
}

// cloud is a single discrete cloud entity.
type cloud struct {
	x     float64     // current horizontal position (pixels, left edge of bounding box)
	baseY float64     // baseline Y in pixel coordinates (flat bottom)
	bumps []cloudBump // circles that define the puffy top
	w     float64     // bounding width
	h     float64     // bounding height (above baseline)
	speed float64     // horizontal drift speed (pixels per frame)
}

// frameKey identifies a drawn frame. The same key gives the same text.
type frameKey struct {
	frame, width, height int
	theme                string
	speed                float64
}

// frameCache keeps the last drawn frame. Bubble Tea draws the screen after
// each message, also between two frames.
type frameCache struct {
	key  frameKey
	text string
	ok   bool
}

func (f *frameCache) get(k frameKey) (string, bool) {
	if f.ok && f.key == k {
		return f.text, true
	}
	return "", false
}

func (f *frameCache) put(k frameKey, text string) string {
	f.key, f.text, f.ok = k, text, true
	return text
}

// halfBlocks draws a grid of width by 2*height pixels. Each cell is "▀",
// with the upper pixel as the foreground and the lower pixel as the
// background. It writes a color sequence only when the colors change from
// the cell before, and resets the colors at the end of each line.
func halfBlocks(width, height int, pixel func(x, y int) color.Color) string {
	params := make(map[uint32]string, 8)
	param := func(c color.Color) (uint32, string) {
		r, g, b, _ := c.RGBA()
		k := (r>>8)<<16 | (g>>8)<<8 | b>>8
		p, ok := params[k]
		if !ok {
			p = fmt.Sprintf("%d;%d;%d", r>>8, g>>8, b>>8)
			params[k] = p
		}
		return k, p
	}

	var b strings.Builder
	b.Grow(width*height*4 + height*40)
	for row := range height {
		var lastFg, lastBg uint32
		for col := range width {
			fk, fp := param(pixel(col, row*2))
			bk, bp := param(pixel(col, row*2+1))
			if col == 0 || fk != lastFg || bk != lastBg {
				b.WriteString("\x1b[38;2;")
				b.WriteString(fp)
				b.WriteString(";48;2;")
				b.WriteString(bp)
				b.WriteByte('m')
				lastFg, lastBg = fk, bk
			}
			b.WriteString("▀")
		}
		b.WriteString("\x1b[m")
		if row < height-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// withSpeed draws the speed label in the lower right corner when the speed
// is not the default.
func withSpeed(frame string, width, height int, speed float64) string {
	if speed == 1.0 {
		return frame
	}
	label := fmt.Sprintf(" %.0fx ", speed)
	if speed != math.Trunc(speed) {
		label = fmt.Sprintf(" %.2gx ", speed)
	}
	indicator := S.Muted.Render(label)
	x := width - lipgloss.Width(indicator) - 1
	y := height - lipgloss.Height(indicator)
	if x <= 0 || y <= 0 {
		return frame
	}
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(frame).Z(0),
		lipgloss.NewLayer(indicator).X(x).Y(y).Z(1),
	).Render()
}

// Speed multiplier bounds.
const (
	speedMin  = 0.25
	speedMax  = 4.0
	speedStep = 0.25
)

// CloudAnimation renders discrete drifting clouds built from circle unions.
type CloudAnimation struct {
	clouds    []cloud
	frame     int
	rng       lcg
	speedMult float64 // drift speed multiplier (default 1.0)
	drawn     frameCache
}

// NewCloudAnimation creates a cloud animation with the given random seed.
func NewCloudAnimation(seed int64) *CloudAnimation {
	rng := lcg(uint64(seed)) //nolint:gosec // seed for visual animation, not crypto
	return &CloudAnimation{rng: rng, speedMult: 1.0}
}

// Update moves the clouds by one frame. A cloud that leaves the screen
// comes back as a new cloud on the right.
func (c *CloudAnimation) Update(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	pixH := height * 2
	if len(c.clouds) == 0 {
		c.clouds = c.generateClouds(width, pixH)
	}
	for i := range c.clouds {
		c.clouds[i].x -= c.clouds[i].speed * c.speedMult
		if c.clouds[i].x+c.clouds[i].w < 0 {
			c.clouds[i] = c.makeCloud(width, pixH, i, len(c.clouds))
			c.clouds[i].x = float64(width) + c.rng.float()*40
		}
	}
	c.frame++
}

func (c *CloudAnimation) SpeedUp() {
	c.speedMult += speedStep
	if c.speedMult > speedMax {
		c.speedMult = speedMax
	}
}

func (c *CloudAnimation) SlowDown() {
	c.speedMult -= speedStep
	if c.speedMult < speedMin {
		c.speedMult = speedMin
	}
}

func (c *CloudAnimation) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	t := ActiveTheme
	key := frameKey{c.frame, width, height, t.Name, c.speedMult}
	if text, ok := c.drawn.get(key); ok {
		return text
	}
	if len(c.clouds) == 0 {
		c.clouds = c.generateClouds(width, height*2)
	}

	// Cloud shading colors (top-lit: highlight on top, shadow on bottom)
	highlight := t.Muted // brightest — top edge
	body := t.Overlay    // mid-tone — interior
	shadow := t.Surface  // darkest cloud color — bottom
	sky := t.Base

	frame := halfBlocks(width, height, func(x, y int) color.Color {
		return c.pixelColor(float64(x), float64(y), sky, highlight, body, shadow)
	})
	return c.drawn.put(key, withSpeed(frame, width, height, c.speedMult))
}

// pixelColor determines the color for a single pixel coordinate.
func (c *CloudAnimation) pixelColor(px, py float64, sky, highlight, body, shadow color.Color) color.Color {
	for i := range c.clouds {
		cl := &c.clouds[i]
		// Quick bounding box check
		if px < cl.x || px > cl.x+cl.w {
			continue
		}
		if py > cl.baseY || py < cl.baseY-cl.h {
			continue
		}

		// Check circle membership
		for _, bump := range cl.bumps {
			bx := cl.x + bump.cx
			by := cl.baseY + bump.cy // cy is negative (above baseline)
			dx := px - bx
			dy := py - by
			if dx*dx+dy*dy <= bump.r*bump.r {
				// Inside cloud — shade based on vertical position
				// 0.0 = at baseline (bottom), 1.0 = at top of cloud
				t := (cl.baseY - py) / cl.h
				if t > 0.65 {
					return highlight
				}
				if t > 0.25 {
					return body
				}
				return shadow
			}
		}
	}
	return sky
}

// generateClouds creates a set of clouds distributed across the screen.
func (c *CloudAnimation) generateClouds(screenW, screenH int) []cloud {
	// Place clouds distributed across the screen
	numClouds := 14 + int(c.rng.float()*6) // 14-19 clouds
	clouds := make([]cloud, numClouds)

	for i := range clouds {
		clouds[i] = c.makeCloud(screenW, screenH, i, numClouds)
	}
	return clouds
}

// makeCloud generates a single cloud with circle-union bumps.
func (c *CloudAnimation) makeCloud(screenW, screenH, idx, total int) cloud {
	// Distribute clouds across the full vertical range
	maxY := float64(screenH) * 0.97
	minY := float64(screenH) * 0.05
	baseY := minY + c.rng.float()*(maxY-minY)

	// Random horizontal starting position spread across the screen
	x := c.rng.float() * float64(screenW)

	// Cloud size: weighted distribution with rare jumbo clouds
	sizeRoll := c.rng.float()
	var numBumps int
	var maxRadius float64
	switch {
	case sizeRoll < 0.08: // 8% chance: jumbo clouds
		numBumps = 9 + int(c.rng.float()*5)  // 9-13 bumps
		maxRadius = 14.0 + c.rng.float()*8.0 // 14-22 pixel radius
	case sizeRoll < 0.30: // 22% chance: large clouds
		numBumps = 6 + int(c.rng.float()*4) // 6-9 bumps
		maxRadius = 8.0 + c.rng.float()*6.0 // 8-14 pixel radius
	case sizeRoll < 0.70: // 40% chance: medium clouds
		numBumps = 4 + int(c.rng.float()*3) // 4-6 bumps
		maxRadius = 4.0 + c.rng.float()*5.0 // 4-9 pixel radius
	default: // 30% chance: small clouds
		numBumps = 3 + int(c.rng.float()*2) // 3-4 bumps
		maxRadius = 2.5 + c.rng.float()*3.0 // 2.5-5.5 pixel radius
	}

	// Speed: larger clouds drift slower (parallax feel)
	speed := 0.08 + c.rng.float()*0.25

	bumps := make([]cloudBump, numBumps)
	cx := 0.0
	cloudH := 0.0
	for j := range bumps {
		// Larger bumps toward center, smaller at edges
		edgeFactor := 1.0 - math.Abs(2.0*float64(j)/float64(numBumps-1)-1.0)
		r := maxRadius * (0.4 + edgeFactor*0.6) // 40-100% of max
		// Slight random variation
		r += (c.rng.float() - 0.5) * 1.5
		if r < 1.5 {
			r = 1.5
		}

		bumps[j] = cloudBump{
			cx: cx + r,   // center X relative to cloud left edge
			cy: -r * 0.6, // center above baseline (negative Y = up)
			r:  r,
		}
		cx += r * 1.3 // overlap ~35%

		bumpTop := r + r*0.6 // how far above baseline this bump reaches
		if bumpTop > cloudH {
			cloudH = bumpTop
		}
	}

	totalW := cx + maxRadius*0.5

	// Spread across screen — offset by index to avoid clustering
	spreadOffset := float64(idx) * float64(screenW) / float64(total)
	x = math.Mod(x+spreadOffset, float64(screenW))

	return cloud{
		x:     x,
		baseY: baseY,
		bumps: bumps,
		w:     totalW,
		h:     cloudH,
		speed: speed,
	}
}

// lcg is a simple linear congruential generator for deterministic pseudo-random numbers.
type lcg uint64

func (l *lcg) float() float64 {
	*l = *l*6364136223846793005 + 1442695040888963407
	return float64(*l>>33) / float64(1<<31)
}

// intn returns a pseudo-random int in [0, n).
func (l *lcg) intn(n int) int {
	return int(l.float() * float64(n))
}

// --- Storm Animation ---

// raindrop is a single falling rain particle.
type raindrop struct {
	x, y  float64
	speed float64
	char  rune // '│', '|', '\'', etc.
}

// lightning represents an active lightning bolt.
type lightning struct {
	segments []lightningPt // points forming the bolt
	ttl      int           // frames remaining
	flashTTL int           // frames of sky flash remaining
}

type lightningPt struct {
	x, y int
}

// StormAnimation renders heavy clouds, rain, and lightning.
type StormAnimation struct {
	clouds    []cloud
	rain      []raindrop
	bolt      *lightning
	frame     int
	rng       lcg
	speedMult float64
	drawn     frameCache
}

// NewStormAnimation creates a storm animation with the given random seed.
func NewStormAnimation(seed int64) *StormAnimation {
	rng := lcg(uint64(seed)) //nolint:gosec // seed for visual animation, not crypto
	return &StormAnimation{rng: rng, speedMult: 1.0}
}

// Update moves the clouds and the rain by one frame, and starts or ends a
// lightning bolt.
func (s *StormAnimation) Update(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	pixH := height * 2
	s.ensure(width, pixH)

	for i := range s.clouds {
		s.clouds[i].x -= s.clouds[i].speed * s.speedMult
		if s.clouds[i].x+s.clouds[i].w < 0 {
			s.clouds[i] = s.makeStormCloud(width, pixH)
			s.clouds[i].x = float64(width) + s.rng.float()*40
		}
	}

	for i := range s.rain {
		s.rain[i].y += s.rain[i].speed * s.speedMult
		if s.rain[i].y >= float64(pixH) {
			s.rain[i].y = -s.rng.float() * 10
			s.rain[i].x = s.rng.float() * float64(width)
		}
		// Wind drift — rain falls slightly to the left
		s.rain[i].x -= 0.15 * s.speedMult
		if s.rain[i].x < 0 {
			s.rain[i].x += float64(width)
		}
	}

	// Lightning: ~2% chance per frame (~once every 2.5 seconds at 20 FPS)
	if s.bolt != nil {
		s.bolt.ttl--
		s.bolt.flashTTL--
		if s.bolt.ttl <= 0 {
			s.bolt = nil
		}
	} else if s.rng.float() < 0.02 {
		s.bolt = s.generateLightning(width, pixH)
	}
	s.frame++
}

// ensure makes the clouds and the rain on the first frame.
func (s *StormAnimation) ensure(width, pixH int) {
	if len(s.clouds) == 0 {
		s.clouds = s.generateStormClouds(width, pixH)
		s.rain = s.generateRain(width, pixH, 120+s.rng.intn(60))
	}
}

func (s *StormAnimation) SpeedUp() {
	s.speedMult += speedStep
	if s.speedMult > speedMax {
		s.speedMult = speedMax
	}
}

func (s *StormAnimation) SlowDown() {
	s.speedMult -= speedStep
	if s.speedMult < speedMin {
		s.speedMult = speedMin
	}
}

func (s *StormAnimation) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	t := ActiveTheme
	key := frameKey{s.frame, width, height, t.Name, s.speedMult}
	if text, ok := s.drawn.get(key); ok {
		return text
	}
	pixH := height * 2
	s.ensure(width, pixH)

	// Storm uses darker shading
	highlight := t.Muted
	body := t.Surface
	shadow := t.Base
	sky := t.Base

	// During flash, brighten the sky
	flashActive := s.bolt != nil && s.bolt.flashTTL > 0
	if flashActive {
		sky = t.Overlay
	}

	// Build rain lookup grid (pixel coords → raindrop info)
	type rainPx struct {
		hit  bool
		char rune
	}
	rainGrid := make([]rainPx, width*pixH)
	for _, r := range s.rain {
		rx := int(r.x) % width
		ry := int(r.y)
		if rx < 0 {
			rx += width
		}
		if ry >= 0 && ry < pixH && rx >= 0 && rx < width {
			rainGrid[ry*width+rx] = rainPx{hit: true, char: r.char}
		}
	}

	// Build lightning lookup
	boltGrid := make(map[int]bool)
	if s.bolt != nil {
		for _, pt := range s.bolt.segments {
			if pt.x >= 0 && pt.x < width && pt.y >= 0 && pt.y < pixH {
				boltGrid[pt.y*width+pt.x] = true
				// Make bolt 2px wide
				if pt.x+1 < width {
					boltGrid[pt.y*width+pt.x+1] = true
				}
			}
		}
	}

	rainColor := t.Secondary
	boltColor := t.Warning

	// pixelColor for storm
	stormPixel := func(px, py int) color.Color {
		// Lightning bolt takes priority
		if boltGrid[py*width+px] {
			return boltColor
		}

		// Check clouds
		fpx, fpy := float64(px), float64(py)
		for i := range s.clouds {
			cl := &s.clouds[i]
			if fpx < cl.x || fpx > cl.x+cl.w {
				continue
			}
			if fpy > cl.baseY || fpy < cl.baseY-cl.h {
				continue
			}
			for _, bump := range cl.bumps {
				bx := cl.x + bump.cx
				by := cl.baseY + bump.cy
				dx := fpx - bx
				dy := fpy - by
				if dx*dx+dy*dy <= bump.r*bump.r {
					vt := (cl.baseY - fpy) / cl.h
					if vt > 0.65 {
						return highlight
					}
					if vt > 0.25 {
						return body
					}
					return shadow
				}
			}
		}

		// Rain
		rp := rainGrid[py*width+px]
		if rp.hit {
			return rainColor
		}

		return sky
	}

	frame := halfBlocks(width, height, stormPixel)
	return s.drawn.put(key, withSpeed(frame, width, height, s.speedMult))
}

func (s *StormAnimation) generateStormClouds(screenW, screenH int) []cloud {
	numClouds := 10 + s.rng.intn(5) // 10-14 heavy clouds
	clouds := make([]cloud, numClouds)
	for i := range clouds {
		clouds[i] = s.makeStormCloud(screenW, screenH)
		// Spread initial positions
		spreadOffset := float64(i) * float64(screenW) / float64(numClouds)
		clouds[i].x = math.Mod(clouds[i].x+spreadOffset, float64(screenW))
	}
	return clouds
}

func (s *StormAnimation) makeStormCloud(screenW, screenH int) cloud {
	// Storm clouds cluster in upper 60% — heavy overcast
	maxY := float64(screenH) * 0.60
	minY := float64(screenH) * 0.03
	baseY := minY + s.rng.float()*(maxY-minY)
	x := s.rng.float() * float64(screenW)

	// Heavily biased toward large and jumbo
	sizeRoll := s.rng.float()
	var numBumps int
	var maxRadius float64
	switch {
	case sizeRoll < 0.30: // 30% jumbo
		numBumps = 9 + s.rng.intn(6)          // 9-14 bumps
		maxRadius = 14.0 + s.rng.float()*10.0 // 14-24 pixel radius
	case sizeRoll < 0.70: // 40% large
		numBumps = 6 + s.rng.intn(4)        // 6-9 bumps
		maxRadius = 8.0 + s.rng.float()*8.0 // 8-16 pixel radius
	default: // 30% medium (no small in storms)
		numBumps = 4 + s.rng.intn(3)        // 4-6 bumps
		maxRadius = 5.0 + s.rng.float()*5.0 // 5-10 pixel radius
	}

	speed := 0.15 + s.rng.float()*0.35 // faster than calm weather

	bumps := make([]cloudBump, numBumps)
	cx := 0.0
	cloudH := 0.0
	for j := range bumps {
		edgeFactor := 1.0 - math.Abs(2.0*float64(j)/float64(numBumps-1)-1.0)
		r := maxRadius * (0.4 + edgeFactor*0.6)
		r += (s.rng.float() - 0.5) * 1.5
		if r < 2.0 {
			r = 2.0
		}
		bumps[j] = cloudBump{
			cx: cx + r,
			cy: -r * 0.6,
			r:  r,
		}
		cx += r * 1.2 // tighter overlap for storm clouds
		bumpTop := r + r*0.6
		if bumpTop > cloudH {
			cloudH = bumpTop
		}
	}

	return cloud{
		x:     x,
		baseY: baseY,
		bumps: bumps,
		w:     cx + maxRadius*0.5,
		h:     cloudH,
		speed: speed,
	}
}

func (s *StormAnimation) generateRain(screenW, screenH, count int) []raindrop {
	chars := []rune{'│', '│', '│', '|', '\'', '·'}
	drops := make([]raindrop, count)
	for i := range drops {
		drops[i] = raindrop{
			x:     s.rng.float() * float64(screenW),
			y:     s.rng.float() * float64(screenH),
			speed: 1.5 + s.rng.float()*2.0,
			char:  chars[s.rng.intn(len(chars))],
		}
	}
	return drops
}

func (s *StormAnimation) generateLightning(screenW, screenH int) *lightning {
	// Pick a random cloud to originate from
	if len(s.clouds) == 0 {
		return nil
	}
	cl := s.clouds[s.rng.intn(len(s.clouds))]

	// Start at the cloud's baseline (bottom), centered horizontally
	x := int(cl.x + cl.w*0.3 + s.rng.float()*cl.w*0.4)
	y := int(cl.baseY)

	if x < 0 || x >= screenW {
		return nil
	}

	// Jagged bolt traveling downward from cloud base
	var segments []lightningPt
	for y < screenH*7/8 {
		segments = append(segments, lightningPt{x, y})
		y += 1 + s.rng.intn(3) // step down 1-3 pixels
		x += s.rng.intn(5) - 2 // drift left/right -2 to +2
		if x < 0 {
			x = 0
		}
		if x >= screenW {
			x = screenW - 1
		}
	}

	return &lightning{
		segments: segments,
		ttl:      6, // visible for 6 frames (~0.3s)
		flashTTL: 3, // sky flash for first 3 frames
	}
}
