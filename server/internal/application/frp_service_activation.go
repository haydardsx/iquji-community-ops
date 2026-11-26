package application

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

func activateManagedFrps(ctx context.Context, client *ssh.Client, input FrpTunnelExecutionInput) error {
	nodeID, err := frpSwarmManagerNodeID(ctx, client)
	if err != nil {
		return frpStartNotActivatedError{err}
	}
	service, err := inspectManagedFrps(ctx, client, nodeID)
	if err != nil {
		return frpStartNotActivatedError{err}
	}
	if !service.Installed {
		return frpStartNotActivatedError{errors.New("FRPS_NOT_INSTALLED")}
	}
	if service.RunID != input.Run.ID && (service.RunID != "" || service.Replicas > 0) {
		return frpStartNotActivatedError{errors.New("FRPS_ALREADY_IN_USE")}
	}
	if service.RunID == input.Run.ID && service.Replicas > 0 {
		if err := remoteRun(ctx, client, frpsSwarmServiceReadyCommand(service.Service)); err != nil {
			return errors.New("FRPS_SWARM_SERVICE_NOT_READY")
		}
		return nil
	}
	if frpUsesTraefik(input.Run.Mappings) {
		if err := remoteRun(ctx, client, frpTraefikNetworkCheckCommand(frpTraefikNetwork(input))); err != nil {
			return frpStartNotActivatedError{errors.New("TRAEFIK_SWARM_NETWORK_REQUIRED")}
		}
	}
	if input.Run.RemoteDir != frpTunnelRemoteDir(input.Run.ID) {
		return frpStartNotActivatedError{errors.New("FRP_REMOTE_DIR_REQUIRED")}
	}
	if err := writeRemoteFile(ctx, client, input.Run.RemoteDir+"/frps.toml", frpsConfig(input)); err != nil {
		return frpStartNotActivatedError{errors.New("FRPS_CONFIG_WRITE_FAILED")}
	}
	if err := writeRemoteFile(ctx, client, input.Run.RemoteDir+"/token", input.Token); err != nil {
		return errors.New("FRPS_TOKEN_WRITE_FAILED")
	}
	input.LogStep("swarm", "activate-frps", "info", "启用已安装的 FRPS 服务")
	if err := remoteRun(ctx, client, strings.Join(frpsActivateArgs(input, service), " ")); err != nil {
		return errors.New("FRPS_SWARM_SERVICE_ACTIVATION_FAILED")
	}
	if err := remoteRun(ctx, client, frpsSwarmServiceReadyCommand(service.Service)); err != nil {
		return errors.New("FRPS_SWARM_SERVICE_NOT_READY")
	}
	return nil
}

func frpsActivateArgs(input FrpTunnelExecutionInput, service managedFrps) []string {
	args := []string{
		"docker service update --detach=true --replicas 1",
		"--label-add " + shellQuote("iquops.tunnelId="+input.Run.TunnelID),
		"--label-add " + shellQuote("iquops.tunnelRunId="+input.Run.ID),
		"--mount-add " + shellQuote("type=bind,source="+input.Run.RemoteDir+",target=/etc/frp,readonly"),
		"--args " + shellQuote("-c /etc/frp/frps.toml"),
	}
	if frpUsesTraefik(input.Run.Mappings) {
		args = append(args, "--network-add "+shellQuote(frpTraefikNetwork(input)))
		for _, label := range frpsTraefikLabels(input) {
			args = append(args, "--label-add "+shellQuote(label))
		}
	} else {
		args = append(args, frpsPublishArg(input.Run.FrpsBindPort))
	}
	for _, mapping := range input.Run.Mappings {
		if mapping.PublicDomain == "" {
			args = append(args, frpsPublishArg(mapping.FrpsProxyPort))
		}
	}
	return append(args, shellQuote(service.ID))
}

func frpsPublishArg(port int) string {
	value := strconv.Itoa(port)
	return "--publish-add " + shellQuote("published="+value+",target="+value+",protocol=tcp,mode=host")
}

func parkRemoteFrps(ctx context.Context, client *ssh.Client, input FrpTunnelExecutionInput) error {
	nodeID, err := frpSwarmManagerNodeID(ctx, client)
	if err != nil {
		return err
	}
	var service managedFrps
	if input.Run.ManagedFrps {
		service, err = inspectManagedFrps(ctx, client, nodeID)
	} else {
		var legacy []managedFrps
		legacy, err = inspectLegacyFrps(ctx, client, nodeID)
		for _, candidate := range legacy {
			if candidate.RunID == input.Run.ID && candidate.Service == input.Run.ContainerName {
				service = candidate
				break
			}
		}
	}
	if err != nil {
		return err
	}
	if service.ID != "" && service.RunID == input.Run.ID && service.labels["iquops.tunnelId"] != input.Run.TunnelID {
		return errors.New("FRPS_SERVICE_OWNERSHIP_MISMATCH")
	}
	if service.ID != "" && service.RunID == input.Run.ID &&
		(input.Run.ManagedFrps || service.Replicas != 0 || service.labels["traefik.enable"] == "true") {
		input.LogStep("swarm", "park-frps", "info", "关闭本次隧道入口并保留 FRPS 安装")
		if err := remoteRun(ctx, client, strings.Join(frpsParkArgs(input, service), " ")); err != nil {
			return errors.New("FRPS_TUNNEL_DEACTIVATION_FAILED")
		}
	}
	if input.Run.RemoteDir == frpTunnelRemoteDir(input.Run.ID) {
		if err := remoteRun(ctx, client, "rm -f -- "+shellQuote(input.Run.RemoteDir+"/token")+" "+shellQuote(input.Run.RemoteDir+"/frps.env")); err != nil {
			return errors.New("FRPS_TOKEN_REVOCATION_FAILED")
		}
	}
	return nil
}

func frpsParkArgs(input FrpTunnelExecutionInput, service managedFrps) []string {
	args := []string{"docker service update --replicas 0 --label-add " + shellQuote("traefik.enable=false")}
	labels := []string{}
	for key := range service.labels {
		if strings.HasPrefix(key, "traefik.") && key != "traefik.enable" {
			labels = append(labels, key)
		}
	}
	sort.Strings(labels)
	for _, label := range labels {
		args = append(args, "--label-rm "+shellQuote(label))
	}
	if input.Run.ManagedFrps {
		args = append(args, "--label-rm iquops.tunnelId", "--label-rm iquops.tunnelRunId", "--mount-rm /etc/frp")
		if network := service.labels["traefik.swarm.network"]; network != "" {
			args = append(args, "--network-rm "+shellQuote(network))
		} else {
			args = append(args, "--publish-rm "+strconv.Itoa(input.Run.FrpsBindPort))
		}
		for _, mapping := range input.Run.Mappings {
			if mapping.PublicDomain == "" {
				args = append(args, "--publish-rm "+strconv.Itoa(mapping.FrpsProxyPort))
			}
		}
	} else {
		args = append(args, "--env-rm FRP_TOKEN", "--mount-rm /etc/frp/frps.toml")
	}
	return append(args, shellQuote(service.ID))
}
