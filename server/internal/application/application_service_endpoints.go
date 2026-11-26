package application

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 一个生产应用的运行快照按节点批量采样，所有消费者复用同一批端点事实。
func (r *Runtime) observeApplicationEndpoints(ctx context.Context, input ApplicationServiceInput, snapshot ApplicationRuntimeSnapshot) {
	requested := map[string]bool{}
	for _, o := range input.Outputs {
		if o["valueSource"] == "service_host" || o["valueSource"] == "service_port" {
			requested[stringValue(o["serviceSlotKey"])] = true
		}
	}
	nodes := map[string][]*ApplicationServiceState{}
	for i := range snapshot.Services {
		service := &snapshot.Services[i]
		if requested[service.ServiceSlotKey] && service.RunningReplicas > 0 && len(service.Ports) > 0 {
			nodes[service.ServerID] = append(nodes[service.ServerID], service)
		}
	}
	if len(nodes) == 0 {
		return
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var group sync.WaitGroup
	for nodeID, services := range nodes {
		group.Go(func() {
			peer, err := r.containerPeer(ctx, state, nodeID)
			if err != nil {
				return
			}
			defer peer.client.Close()
			host := serverPublicHost(findObject(state.Servers, nodeID))
			command, ports := applicationPortProbe(input.Network, host, services)
			if len(ports) == 0 {
				return
			}
			output, err := peer.call(ctx, command, nil, "APPLICATION_ENDPOINT_PROBE_UNAVAILABLE")
			if err != nil {
				return
			}
			for _, line := range strings.Fields(output) {
				index, mode, ok := strings.Cut(line, "/")
				i, err := strconv.Atoi(index)
				if ok && err == nil && i >= 0 && i < len(ports) {
					switch mode {
					case "internal":
						ports[i]["internalVerified"] = true
					case "published":
						ports[i]["publishedVerified"] = true
						ports[i]["publishedHost"] = host
					}
				}
			}
		})
	}
	group.Wait()
}

// 在运行中副本的网络命名空间内用 overlay 地址探测后端，排除只监听回环的进程。
// 发布映射另从节点网络验证，UDP 没有通用握手，不能用映射声明证明监听。
func applicationPortProbe(network, host string, services []*ApplicationServiceState) (string, []domain.Object) {
	if !applicationResourceName.MatchString(network) {
		return "", nil
	}
	var script strings.Builder
	script.WriteString(`tcp_open() {
	host=$1
	port=$2
	shift 2
	case "$host" in *:*) host="[$host]";; esac
	elapsed=$("$@" curl --silent --output /dev/null --write-out '%{time_connect}' --noproxy '*' --connect-timeout 1 --max-time 1 -- "telnet://$host:$port" </dev/null 2>/dev/null)
	awk -v n="$elapsed" 'BEGIN {exit !(n+0 > 0)}'
}
probe_port() {
	index=$1
	target=$2
	published=$3
	peer_count=0
	for pair in $peers; do
		pid=${pair%%|*}
		ip=${pair#*|}
		tcp_open "$ip" "$target" nsenter -t "$pid" -n || return
		peer_count=$((peer_count+1))
	done
	[ "$peer_count" -eq "$expected" ] || return
	echo "$index/internal"
	if [ -n "$node_host" ] && [ "$published" -gt 0 ] && tcp_open "$node_host" "$published"; then
		echo "$index/published"
	fi
}
`)
	fmt.Fprintf(&script, "node_host=%s\n", shellQuote(host))
	format := "{{.State.Pid}}|{{with index .NetworkSettings.Networks " + strconv.Quote(network) + "}}{{.IPAddress}}{{end}}"
	ports := []domain.Object{}
	for _, service := range services {
		ids := []string{}
		for _, t := range service.Tasks {
			if t.Status == "running" && t.ServerID == service.ServerID {
				ids = append(ids, t.ID)
			}
		}
		if len(ids) == 0 || len(ids) != service.RunningReplicas {
			continue
		}
		fmt.Fprintf(&script, "tasks=%s\nexpected=%d\n", shellQuote(strings.Join(ids, " ")), len(ids))
		// 每个服务只查一次副本地址和 PID，探测进程只进入网络命名空间。
		fmt.Fprintf(&script, `peers=""
for t in $tasks; do
	cid=$(docker ps -q --filter "label=com.docker.swarm.task.id=$t")
	[ -n "$cid" ] || break
	addr=$(docker inspect --format %s "$cid")
	case "$addr" in ''|*'|') break;; esac
	peers="$peers $addr"
done
`, shellQuote(format))
		for _, port := range service.Ports {
			if port["protocol"] != "http" && port["protocol"] != "tcp" {
				continue
			}
			target, published := numberValue(port["containerPort"]), numberValue(port["publishedPort"])
			if target < 1 || target > 65535 {
				continue
			}
			index := len(ports)
			ports = append(ports, port)
			fmt.Fprintf(&script, "probe_port %d %d %d &\n", index, target, published)
		}
	}
	script.WriteString("wait")
	return script.String(), ports
}
