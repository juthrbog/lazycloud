//go:build smoke

package smoke

import "testing"

// :ecs NAME opens the detail of the seeded cluster, enter opens its
// services, and d opens the detail of web with its deployments
// (docs/design/ecs.md, PR 2).
func TestECSClusterServicesAndDetail(t *testing.T) {
	h := start(t, "")
	h.keys(":ecs prod-cluster")
	h.enter()
	h.waitFor("Insights", "prod-cluster")

	h.esc() // close the panel
	h.waitGone("Insights")
	h.enter()
	h.waitFor("prod-cluster services", "3/3 services")
	h.waitFor("web", "worker", "scheduler")

	h.filter("web")
	h.keys("d")
	h.waitFor("Circuit breaker", "on, with rollback")
	h.keys("2")
	h.waitFor("PRIMARY", "web:1")
}
