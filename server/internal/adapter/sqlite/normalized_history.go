package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func rebuildNormalizedHistoryConn(ctx context.Context, conn *sql.Conn, state domain.State) error {
	for _, statement := range []string{
		"DELETE FROM metric_samples",
		"DELETE FROM metric_aggregates",
		"DELETE FROM swarm_snapshots",
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	for _, sample := range state.MetricSamples {
		if err := insertNormalizedMetricSample(ctx, conn, sample); err != nil {
			return err
		}
	}
	for _, aggregate := range state.MetricAggregates {
		if err := insertNormalizedMetricAggregate(ctx, conn, aggregate); err != nil {
			return err
		}
	}
	for _, snapshot := range state.SwarmSnapshots {
		if err := insertNormalizedSwarmSnapshot(ctx, conn, snapshot); err != nil {
			return err
		}
	}
	return nil
}

func insertNormalizedMetricSample(ctx context.Context, conn *sql.Conn, sample domain.Object) error {
	id, err := requiredNormalizedHistoryString("metric_sample", sample, "id")
	if err != nil {
		return err
	}
	serverID, err := requiredNormalizedHistoryString("metric_sample", sample, "serverId")
	if err != nil {
		return err
	}
	targetKind, err := requiredNormalizedHistoryString("metric_sample", sample, "targetKind")
	if err != nil {
		return err
	}
	targetID, err := requiredNormalizedHistoryString("metric_sample", sample, "targetId")
	if err != nil {
		return err
	}
	sampledAt, err := requiredNormalizedHistoryString("metric_sample", sample, "sampledAt")
	if err != nil {
		return err
	}
	values, err := normalizedHistoryJSON(sample["values"], domain.Object{})
	if err != nil {
		return fmt.Errorf("NORMALIZED_METRIC_SAMPLE_VALUES_INVALID:%s: %w", id, err)
	}
	labels, err := normalizedHistoryJSON(sample["labels"], domain.Object{})
	if err != nil {
		return fmt.Errorf("NORMALIZED_METRIC_SAMPLE_LABELS_INVALID:%s: %w", id, err)
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO metric_samples
		(id, server_id, target_kind, target_id, project_id, application_id,
		 application_instance_id, swarm_service_id, swarm_task_id, sampled_at, "values", labels)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		serverID,
		targetKind,
		targetID,
		normalizedNullableString(sample["projectId"]),
		normalizedNullableString(sample["applicationId"]),
		normalizedNullableString(sample["applicationInstanceId"]),
		normalizedNullableString(sample["swarmServiceId"]),
		normalizedNullableString(sample["swarmTaskId"]),
		sampledAt,
		values,
		labels,
	)
	return err
}

func insertNormalizedMetricAggregate(ctx context.Context, conn *sql.Conn, aggregate domain.Object) error {
	id, err := requiredNormalizedHistoryString("metric_aggregate", aggregate, "id")
	if err != nil {
		return err
	}
	serverID, err := requiredNormalizedHistoryString("metric_aggregate", aggregate, "serverId")
	if err != nil {
		return err
	}
	targetKind, err := requiredNormalizedHistoryString("metric_aggregate", aggregate, "targetKind")
	if err != nil {
		return err
	}
	targetID, err := requiredNormalizedHistoryString("metric_aggregate", aggregate, "targetId")
	if err != nil {
		return err
	}
	window, err := requiredNormalizedHistoryString("metric_aggregate", aggregate, "window")
	if err != nil {
		return err
	}
	bucketStart, err := requiredNormalizedHistoryString("metric_aggregate", aggregate, "bucketStart")
	if err != nil {
		return err
	}
	values, err := normalizedHistoryJSON(aggregate["values"], domain.Object{})
	if err != nil {
		return fmt.Errorf("NORMALIZED_METRIC_AGGREGATE_VALUES_INVALID:%s: %w", id, err)
	}
	labels, err := normalizedHistoryJSON(aggregate["labels"], domain.Object{})
	if err != nil {
		return fmt.Errorf("NORMALIZED_METRIC_AGGREGATE_LABELS_INVALID:%s: %w", id, err)
	}
	createdAt := strings.TrimSpace(domain.StringValue(aggregate["createdAt"]))
	if createdAt == "" {
		createdAt = domain.Now()
	}
	updatedAt := strings.TrimSpace(domain.StringValue(aggregate["updatedAt"]))
	if updatedAt == "" {
		updatedAt = createdAt
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO metric_aggregates
		(id, server_id, target_kind, target_id, project_id, application_id,
		 application_instance_id, swarm_service_id, swarm_task_id, "window", bucket_start,
		 "values", labels, sample_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		serverID,
		targetKind,
		targetID,
		normalizedNullableString(aggregate["projectId"]),
		normalizedNullableString(aggregate["applicationId"]),
		normalizedNullableString(aggregate["applicationInstanceId"]),
		normalizedNullableString(aggregate["swarmServiceId"]),
		normalizedNullableString(aggregate["swarmTaskId"]),
		window,
		bucketStart,
		values,
		labels,
		domain.NumberValue(aggregate["sampleCount"]),
		createdAt,
		updatedAt,
	)
	return err
}

func insertNormalizedSwarmSnapshot(ctx context.Context, conn *sql.Conn, snapshot domain.Object) error {
	id, err := requiredNormalizedHistoryString("swarm_snapshot", snapshot, "id")
	if err != nil {
		return err
	}
	serverID, err := requiredNormalizedHistoryString("swarm_snapshot", snapshot, "serverId")
	if err != nil {
		return err
	}
	sampledAt, err := requiredNormalizedHistoryString("swarm_snapshot", snapshot, "sampledAt")
	if err != nil {
		return err
	}
	summary, err := requiredNormalizedHistoryString("swarm_snapshot", snapshot, "summary")
	if err != nil {
		return err
	}
	status, err := requiredNormalizedHistoryString("swarm_snapshot", snapshot, "status")
	if err != nil {
		return err
	}
	nodes, err := normalizedHistoryJSON(snapshot["nodes"], []domain.Object{})
	if err != nil {
		return fmt.Errorf("NORMALIZED_SWARM_SNAPSHOT_NODES_INVALID:%s: %w", id, err)
	}
	services, err := normalizedHistoryJSON(snapshot["services"], []domain.Object{})
	if err != nil {
		return fmt.Errorf("NORMALIZED_SWARM_SNAPSHOT_SERVICES_INVALID:%s: %w", id, err)
	}
	tasks, err := normalizedHistoryJSON(snapshot["tasks"], []domain.Object{})
	if err != nil {
		return fmt.Errorf("NORMALIZED_SWARM_SNAPSHOT_TASKS_INVALID:%s: %w", id, err)
	}
	networks, err := normalizedHistoryJSON(snapshot["networks"], []domain.Object{})
	if err != nil {
		return fmt.Errorf("NORMALIZED_SWARM_SNAPSHOT_NETWORKS_INVALID:%s: %w", id, err)
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO swarm_snapshots
		(id, server_id, sampled_at, nodes, services, tasks, networks, summary, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, serverID, sampledAt, nodes, services, tasks, networks, summary, status)
	return err
}

func requiredNormalizedHistoryString(kind string, item domain.Object, key string) (string, error) {
	value := strings.TrimSpace(domain.StringValue(item[key]))
	if value == "" {
		return "", fmt.Errorf("NORMALIZED_%s_%s_REQUIRED", strings.ToUpper(kind), strings.ToUpper(key))
	}
	return value, nil
}

func normalizedHistoryJSON(value any, fallback any) (string, error) {
	if value == nil {
		value = fallback
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func normalizedNullableString(value any) any {
	text := strings.TrimSpace(domain.StringValue(value))
	if text == "" {
		return nil
	}
	return text
}
