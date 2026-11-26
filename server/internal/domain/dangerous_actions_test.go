package domain

import "testing"

func TestDangerousActionsRequireConfirmation(t *testing.T) {
	for _, action := range []string{
		"request_jenkins_release",
		" REQUEST_JENKINS_RELEASE ",
		"stop_frp_tunnel",
		"install_frps",
		"uninstall_frps",
		"restore_backup",
		"restore_backup_overwrite",
		"clear_logs",
		"factory_reset",
	} {
		if !IsDangerousAction(action) {
			t.Fatalf("expected dangerous action: %q", action)
		}
	}
	if IsDangerousAction("refresh_inventory") {
		t.Fatal("safe action was classified as dangerous")
	}
}
