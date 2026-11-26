package amqp

import (
	"testing"

	"github.com/shyun-cc/shw-gf-lib/task"
)

func TestPlatformTopology(t *testing.T) {
	topo := PlatformTopology("example.dev", []string{task.DefaultClass, "batch"}, true)
	if len(topo.Exchanges) != 2 || topo.Exchanges[0].Name != "example.dev.task" || topo.Exchanges[0].Kind != "direct" || !topo.Exchanges[0].Durable {
		t.Fatalf("%+v", topo.Exchanges)
	}
	if len(topo.Queues) != 3 || topo.Queues[0].Name != "example.dev.task.dead" {
		t.Fatalf("%+v", topo.Queues)
	}
	q := topo.Queues[2]
	if q.Name != "example.dev.task.batch" || !q.Durable || q.Args["x-dead-letter-exchange"] != "example.dev.task.dlx" || q.Args["x-queue-type"] != "quorum" {
		t.Fatalf("%+v", q)
	}
	if b := topo.Binds[2]; b.Key != "batch" || b.Exchange != "example.dev.task" {
		t.Fatalf("%+v", b)
	}
	if _, ok := PlatformTopology("p", []string{"x"}, false).Queues[1].Args["x-queue-type"]; ok {
		t.Fatal("classic queue must not declare quorum")
	}
}
