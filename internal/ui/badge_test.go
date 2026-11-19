package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func plainHeader(d HeaderData) string { return ansi.Strip(RenderHeader(d)) }

// useFonts sets UseNerdFonts for one test.
func useFonts(t *testing.T, on bool) {
	t.Helper()
	before := UseNerdFonts
	UseNerdFonts = on
	t.Cleanup(func() { UseNerdFonts = before })
}

func TestModeBadge(t *testing.T) {
	useFonts(t, false)
	assert.Equal(t, "  RO  ", ansi.Strip(ModeBadge("RO", false)), "HeaderStyle adds its own padding")
	assert.Equal(t, "  RO*  ", ansi.Strip(ModeBadge("RO", true)))
	assert.Equal(t, " RW ", ansi.Strip(ModeBadge("RW", false)))
	assert.Equal(t, " RW ", ansi.Strip(ModeBadge("RW", true)), "RW cannot be locked")

	useFonts(t, true)
	assert.Equal(t, "  RO \U000f033e  ", ansi.Strip(ModeBadge("RO", true)))
}

func TestLabelBadge(t *testing.T) {
	assert.Empty(t, LabelBadge("", true))
	assert.Equal(t, " PROD ", ansi.Strip(LabelBadge("PROD", true)))
	assert.NotContains(t, ansi.Strip(LabelBadge("a\x1b[31mb", false)), "\x1b", "the label is sanitized")
	assert.NotEqual(t, LabelBadge("PROD", true), LabelBadge("PROD", false), "a locking label has its own color")
}

// The mode is on screen at every width, and the label comes before the
// profile.
func TestHeaderShowsTheModeAndTheLabel(t *testing.T) {
	useFonts(t, false)
	for _, w := range []int{40, 50, 70, 100, 130} {
		got := plainHeader(HeaderData{Profile: "prod-admin", Region: "eu-west-1", Mode: "RO", Locked: true, Label: "PROD", LabelLocks: true, Width: w})
		assert.Contains(t, got, " RO* ", "width %d", w)
		assert.Contains(t, got, " PROD ", "width %d", w)
	}
	got := plainHeader(HeaderData{Profile: "prod-admin", Region: "eu-west-1", Mode: "RO", Label: "PROD", Width: 100})
	assert.Less(t, strings.Index(got, "PROD"), strings.Index(got, "prod-admin"))
	assert.Contains(t, got, "eu-west-1")
	got = plainHeader(HeaderData{Profile: "prod-admin", Region: "eu-west-1", Mode: "RO", Label: "PROD", Width: 70})
	assert.Less(t, strings.Index(got, "PROD"), strings.Index(got, "prod-admin"))

	narrow := plainHeader(HeaderData{Profile: "p", Mode: "RW", Breadcrumbs: []string{"S3", "bucket"}, Width: 50})
	assert.Contains(t, narrow, " RW ")
	assert.Contains(t, narrow, "bucket")
	assert.NotContains(t, narrow, "PROD")
	assert.LessOrEqual(t, ansi.StringWidth(strings.Split(RenderHeader(HeaderData{Mode: "RO", Label: "PROD", Breadcrumbs: []string{"a-very-long-breadcrumb", "another-long-breadcrumb"}, Width: 50}), "\n")[0]), 50)
}

// The status bar starts with the session badge, also with an error, and
// keeps its width.
func TestStatusBarSessionBadge(t *testing.T) {
	useFonts(t, false)
	badge := LabelBadge("PROD", true) + " " + ModeBadge("RO", true)
	keys := []HintBinding{NewHintBinding([]string{"q"}, "q", "quit")}

	got := RenderStatusBar(StatusBarData{Keys: keys, Width: 60, Session: badge})
	assert.Equal(t, 60, ansi.StringWidth(got))
	assert.True(t, strings.HasPrefix(ansi.Strip(got), " PROD    RO*   q quit"), ansi.Strip(got))

	got = RenderStatusBar(StatusBarData{Error: "boom", Width: 60, Session: badge})
	assert.Equal(t, 60, ansi.StringWidth(got))
	assert.True(t, strings.HasPrefix(ansi.Strip(got), " PROD    RO*    boom "), ansi.Strip(got))

	got = RenderStatusBar(StatusBarData{Keys: keys, Width: 60})
	assert.Equal(t, 60, ansi.StringWidth(got))
	assert.NotContains(t, ansi.Strip(got), "RO")
}

// headerLine is the first line of the header, without styles.
func headerLine(d HeaderData) string {
	return ansi.Strip(strings.Split(RenderHeader(d), "\n")[0])
}

// The header shows more of the session as the terminal gets wider.
func TestHeaderTiers(t *testing.T) {
	useFonts(t, false)
	d := HeaderData{Profile: "dev", Region: "eu-west-1", Mode: "RO", Label: "LIVE",
		Account: "123456789012", AccountState: AccountKnown, Principal: "Admin"}
	for _, tc := range []struct {
		width     int
		want, not []string
	}{
		{50, []string{"LIVE", " RO "}, []string{"dev", "123456789012", "eu-west-1", "LazyCloud"}},
		{70, []string{"LIVE", "dev", " RO "}, []string{"123456789012", "eu-west-1", "LazyCloud"}},
		{100, []string{"LIVE", "dev", "123456789012", "eu-west-1", " RO "}, []string{"Admin", "LazyCloud"}},
		{130, []string{"LazyCloud", "LIVE", "dev", "123456789012", "Admin", "eu-west-1", " RO "}, nil},
	} {
		d.Width = tc.width
		got := headerLine(d)
		for _, w := range tc.want {
			assert.Contains(t, got, w, "width %d", tc.width)
		}
		for _, n := range tc.not {
			assert.NotContains(t, got, n, "width %d", tc.width)
		}
		assert.Equal(t, tc.width, ansi.StringWidth(got), "width %d", tc.width)
	}
}

// A long profile and a long principal are cut, so the mode and the
// breadcrumbs stay on the line.
func TestHeaderCutsLongNames(t *testing.T) {
	useFonts(t, false)
	profile := strings.Repeat("p", 40)
	principal := strings.Repeat("r", 60)
	for _, w := range []int{60, 80, 120} {
		got := headerLine(HeaderData{Profile: profile, Principal: principal, Region: "eu-west-1", Mode: "RO", Locked: true,
			Account: "123456789012", AccountState: AccountKnown, Breadcrumbs: []string{"S3", "bucket"}, Width: w})
		assert.Equal(t, w, ansi.StringWidth(got), "width %d", w)
		assert.Contains(t, got, " RO* ", "width %d", w)
		assert.Contains(t, got, "bucket", "width %d", w)
		assert.Contains(t, got, "p…", "width %d", w)
		assert.NotContains(t, got, profile, "width %d", w)
	}
	got := headerLine(HeaderData{Profile: "dev", Principal: principal, Mode: "RO", AccountState: AccountKnown, Width: 160})
	assert.Contains(t, got, strings.Repeat("r", maxHeaderPrincipal-1)+"…")
	assert.NotContains(t, got, strings.Repeat("r", maxHeaderPrincipal))
	got = headerLine(HeaderData{Profile: profile, Mode: "RO", Width: 160})
	assert.Contains(t, got, strings.Repeat("p", maxHeaderProfile-1)+"…", "a wide terminal cuts at the maximum")
	got = headerLine(HeaderData{Profile: "prod-admin", Mode: "RO", Width: 100})
	assert.Contains(t, got, " prod-admin ", "a short profile stays whole")
}

// Each state of the account check has its own text.
func TestHeaderAccountStates(t *testing.T) {
	for _, tc := range []struct {
		d    HeaderData
		want string
	}{
		{HeaderData{AccountState: AccountChecking}, " account? "},
		{HeaderData{AccountState: AccountChecking, Account: "111111111111"}, " 111111111111? "},
		{HeaderData{AccountState: AccountKnown, Account: "111111111111"}, " 111111111111 "},
		{HeaderData{AccountState: AccountLoginNeeded}, " login needed "},
		{HeaderData{AccountState: AccountUnknown}, " account unknown "},
		{HeaderData{AccountState: AccountKnown, Account: "000000000000", Endpoint: true}, "000000000000   endpoint "},
	} {
		tc.d.Mode, tc.d.Width = "RO", 100
		assert.Contains(t, headerLine(tc.d), tc.want)
	}
	raw := RenderHeader(HeaderData{AccountState: AccountKnown, Account: "1\x1b[31m2", Principal: "a\x1b]52;c;x\x07b",
		Region: "r\x1b]0;t\x07", Mode: "RO", Width: 130})
	assert.NotContains(t, raw, "\x1b[31m2", "the account is sanitized")
	assert.NotContains(t, raw, "\x1b]", "the principal and the region are sanitized")
}

// At every width, the header line fills the terminal exactly and shows
// the label and the mode, whatever the session shows. Parts that do not
// fit go away.
func TestHeaderNeverOverflows(t *testing.T) {
	useFonts(t, true)
	cases := []HeaderData{
		{Profile: "prod", Label: "PRODUCTI", Locked: true, AccountState: AccountKnown, Account: "123456789012",
			Principal: "AdministratorAccess-ReadOnly", Region: "ap-southeast-2", Breadcrumbs: []string{"Lambda", "my-function"},
			CrossNavBackLabel: "my-bucket"},
		{Profile: "localstack-very-long-profile-name", Label: "PRODEU", AccountState: AccountChecking, Account: "000000000000",
			Endpoint: true, Region: "ap-southeast-2"},
		{Profile: "localstack", Label: "PRODEU", AccountState: AccountUnknown, Endpoint: true, Region: "ap-southeast-2",
			Breadcrumbs: []string{"S3", "bucket"}},
		{Profile: "dev", AccountState: AccountLoginNeeded, Region: "eu-central-1",
			Breadcrumbs: []string{"S3", strings.Repeat("k", 80)}, CrossNavBackLabel: strings.Repeat("b", 30)},
		{Profile: strings.Repeat("p", 40), Principal: strings.Repeat("r", 60), AccountState: AccountKnown, Account: "123456789012"},
		{Profile: "prod", Label: "PRODUCTI", AccountState: AccountKnown, Account: "123456789012",
			Principal: "AdministratorAccess-ReadOnly", Region: "ap-southeast-2", CrossNavBackLabel: strings.Repeat("b", 40)},
	}
	for i, d := range cases {
		d.Mode = "RO"
		for w := 30; w <= 200; w++ {
			d.Width = w
			got := headerLine(d)
			require.Equal(t, w, ansi.StringWidth(got), "case %d width %d: %q", i, w, got)
			require.Contains(t, got, " RO", "case %d width %d", i, w)
			if d.Label != "" {
				require.Contains(t, got, d.Label, "case %d width %d", i, w)
			}
			// The principal and the title go before the account.
			if d.Account != "" && !strings.Contains(got, d.Account) {
				require.NotContains(t, got, "LazyCloud", "case %d width %d", i, w)
				if d.Principal != "" {
					require.NotContains(t, got, d.Principal[:8], "case %d width %d", i, w)
				}
			}
		}
	}
}

// Without breadcrumbs, a cut profile leaves the line at the width of the
// terminal: the profile badge has the padding of HeaderStyle too.
func TestHeaderCutProfileWithoutCrumbs(t *testing.T) {
	useFonts(t, false)
	d := HeaderData{Profile: strings.Repeat("p", 40), Label: "PRODUCTI", Region: "ap-southeast-2", Mode: "RO",
		AccountState: AccountChecking, Account: "000000000000", Endpoint: true, Width: 80}
	got := headerLine(d)
	assert.Equal(t, 80, ansi.StringWidth(got))
	assert.Contains(t, got, " "+strings.Repeat("p", 13)+"… ", "the profile gets the 14 cells that are left")
	// The profile takes all the room that is left, so the line ends with
	// the mode and its two spaces.
	assert.True(t, strings.HasSuffix(got, " RO    "), "%q", got)
}

// With breadcrumbs, the profile leaves them some room.
func TestHeaderProfileLeavesRoomForCrumbs(t *testing.T) {
	useFonts(t, false)
	d := HeaderData{Profile: strings.Repeat("p", 40), Label: "PRODUCTI", Region: "ap-southeast-2", Mode: "RO",
		AccountState: AccountChecking, Account: "000000000000", Endpoint: true, Breadcrumbs: []string{"S3", "logs-bucket"}, Width: 80}
	got := headerLine(d)
	assert.Equal(t, 80, ansi.StringWidth(got))
	assert.Contains(t, got, " ppp… ", "the profile gets the shortest width")
	assert.Contains(t, got, "logs-buck…", "the crumb gets the rest")
}
