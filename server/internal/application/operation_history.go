package application

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// ListOperationRuns 复用活动归属规则，历史列表使用稳定倒序和真实分页。
func (r *Runtime) ListOperationRuns(ctx context.Context, query TaskQuery) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	items := []domain.OperationRun{}
	for _, run := range state.OperationRuns {
		if query.Action != "" && !strings.Contains(strings.ToLower(run.Action), strings.ToLower(query.Action)) {
			continue
		}
		if query.Status != "" && run.Status != query.Status {
			continue
		}
		if query.Target != "" && !strings.Contains(strings.ToLower(run.TargetKind+" "+run.TargetID+" "+run.TargetName), strings.ToLower(query.Target)) {
			continue
		}
		if !operationMatchesScope(operationScope(state, run), query) {
			continue
		}
		items = append(items, run)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt == items[j].CreatedAt {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt > items[j].CreatedAt
	})
	page, pageSize, start, end := operationPage(query.Page, query.PageSize, len(items))
	result := make([]map[string]any, 0, end-start)
	for _, run := range items[start:end] {
		result = append(result, operationActivityResponse(state, run))
	}
	return map[string]any{"items": result, "page": page, "pageSize": pageSize, "total": len(items)}, nil
}

func operationPage(page, pageSize, total int) (int, int, int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	start := total
	if page <= total/pageSize+1 {
		start = (page - 1) * pageSize
	}
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return page, pageSize, start, end
}

// ListOperationLogPage 展示同步动作与任务事件的历史，不为同步动作制造执行任务。
func (r *Runtime) ListOperationLogPage(ctx context.Context, query TaskQuery) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	items := []domain.OperationLog{}
	for _, entry := range state.OperationLogs {
		if query.Status != "" && entry.Status != query.Status {
			continue
		}
		if query.Action != "" && !strings.Contains(strings.ToLower(entry.Action), strings.ToLower(query.Action)) {
			continue
		}
		run := domain.OperationRun{ID: entry.OperationRunID, Action: entry.Action, TargetKind: entry.TargetKind, TargetID: entry.TargetID}
		if !operationMatchesScope(operationScope(state, run), query) {
			continue
		}
		items = append(items, entry)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return logRowPrecedes(items[j].Timestamp, items[i].Timestamp, items[j].ID, items[i].ID)
	})
	page, pageSize, start, end := operationPage(query.Page, query.PageSize, len(items))
	return map[string]any{"items": items[start:end], "page": page, "pageSize": pageSize, "total": len(items)}, nil
}

func (r *Runtime) ListRuntimeLogs(ctx context.Context, targetKind, targetID, operationRunID string) ([]domain.RuntimeLog, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	items := []domain.RuntimeLog{}
	for _, entry := range state.RuntimeLogs {
		if targetKind != "" && entry.TargetKind != targetKind {
			continue
		}
		if targetID != "" && entry.TargetID != targetID {
			continue
		}
		if operationRunID != "" && entry.OperationRunID != operationRunID {
			continue
		}
		items = append(items, entry)
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		return logRowPrecedes(left.Timestamp, right.Timestamp, left.ID, right.ID)
	})
	return items, nil
}

func (r *Runtime) ListRuntimeLogsPage(ctx context.Context, targetKind, targetID, operationRunID string, page, pageSize int, order string) (map[string]any, error) {
	items, err := r.ListRuntimeLogs(ctx, targetKind, targetID, operationRunID)
	if err != nil {
		return nil, err
	}
	if order == "desc" {
		slices.Reverse(items)
	}
	page, pageSize, start, end := operationPage(page, pageSize, len(items))
	return map[string]any{"items": items[start:end], "page": page, "pageSize": pageSize, "total": len(items)}, nil
}

func logRowPrecedes(leftTime, rightTime, leftID, rightID string) bool {
	left, _ := time.Parse(time.RFC3339Nano, leftTime)
	right, _ := time.Parse(time.RFC3339Nano, rightTime)
	if left.Equal(right) {
		return leftID < rightID
	}
	return left.Before(right)
}
