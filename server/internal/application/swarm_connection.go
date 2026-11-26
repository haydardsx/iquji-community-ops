package application

import (
	"errors"
	"net"
	"strings"
)

// 显式连接方式与真实通告地址类型使用同一校验，公网 NAT 地址无需出现在本机网卡。
func ValidateSwarmConnectionInput(mode, address string, required bool) error {
	mode = strings.ToLower(strings.TrimSpace(mode))
	address = strings.TrimSpace(address)
	if mode != "" && mode != "public" && mode != "private" || required && mode == "" {
		return errors.New("SWARM_CONNECTION_MODE_INVALID")
	}
	if address == "" {
		if mode != "" || required {
			return errors.New("SWARM_ADVERTISE_ADDRESS_REQUIRED")
		}
		return nil
	}
	ip := net.ParseIP(address)
	if ip == nil || swarmAddressConnectionMode(ip.String()) == "unknown" {
		return errors.New("SWARM_ADVERTISE_ADDRESS_INVALID")
	}
	if mode != "" && mode != swarmAddressConnectionMode(address) {
		return errors.New("SWARM_CONNECTION_ADDRESS_MODE_MISMATCH")
	}
	return nil
}

func swarmAddressConnectionMode(address string) string {
	if host, _, err := net.SplitHostPort(address); err == nil {
		address = host
	}
	ip := net.ParseIP(strings.TrimSpace(address))
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return "unknown"
	}
	v4 := ip.To4()
	if v4 != nil && (v4[0] == 0 || v4[0] >= 224) {
		return "unknown"
	}
	if ip.IsPrivate() || v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return "private"
	}
	return "public"
}

func swarmAdvertiseAddress(input ServerRuntimeExecutionInput) (string, error) {
	address := strings.TrimSpace(input.SwarmAdvertiseAddr)
	if address == "" && input.SwarmConnectionMode == "public" {
		address = stringValue(input.Server["publicHost"])
	}
	if address == "" {
		address = firstNonEmptyString(stringValue(input.Server["privateHost"]), stringValue(input.Server["publicHost"]))
	}
	if err := ValidateSwarmConnectionInput(input.SwarmConnectionMode, address, false); err != nil {
		return "", err
	}
	return net.ParseIP(address).String(), nil
}

func verifyRequestedSwarmConnection(input ServerRuntimeExecutionInput, status remoteSwarmStatus) error {
	if input.SwarmConnectionMode == "" && input.SwarmAdvertiseAddr == "" {
		return nil
	}
	actual := status.AdvertiseAddr
	if status.Role == "manager" {
		actual, _, _ = net.SplitHostPort(status.ManagerAddress)
	}
	if input.SwarmConnectionMode != "" && swarmAddressConnectionMode(actual) != input.SwarmConnectionMode {
		return errors.New("SWARM_CONNECTION_RECONFIGURE_REQUIRED")
	}
	if input.SwarmAdvertiseAddr != "" && (net.ParseIP(actual) == nil || !net.ParseIP(actual).Equal(net.ParseIP(input.SwarmAdvertiseAddr))) {
		return errors.New("SWARM_CONNECTION_RECONFIGURE_REQUIRED")
	}
	return nil
}
