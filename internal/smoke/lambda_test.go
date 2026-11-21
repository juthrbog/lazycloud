//go:build smoke

package smoke

import "testing"

// :lambda NAME opens the detail of a seeded function with hidden values,
// $ shows the values, and enter opens the log group of the function with
// the seeded invocation lines (docs/design/lambda.md, PR 2).
func TestLambdaFunctionDetailAndLogs(t *testing.T) {
	h := start(t, "")
	h.keys(":lambda orders-api")
	h.enter()
	h.waitFor("Last update", "Active")
	h.keys("2")
	h.waitFor("Values are hidden", "DB_PASSWORD")
	h.waitGone("not-a-real-secret")

	h.esc() // close the panel
	h.waitGone("Last update")
	h.keys("$")
	h.waitFor("not-a-real-secret")

	h.esc()
	h.waitGone("not-a-real-secret")
	h.enter()
	h.waitFor("REPORT RequestId")
}
