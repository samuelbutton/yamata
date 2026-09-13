package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

func sameJob(a, b object) bool {
	for _, key := range []string{"execution_id", "job_id", "correlation_id"} {
		if text(a, key) != text(b, key) {
			return false
		}
	}
	return true
}
func (g *graph) relations(ctx context.Context, d object) error {
	switch text(d, "kind") {
	case "job":
		hash, err := hashObject(d["inputs"])
		if err != nil {
			return err
		}
		if hash != text(d, "inputs_hash") {
			return errors.New("input hash mismatch")
		}
		if text(d, "job_kind") == "analysis" {
			bag, err := g.reference(ctx, nested(nested(d, "inputs"), "bag"), "bag")
			if err != nil {
				return err
			}
			if text(bag.doc, "execution_id") != text(d, "execution_id") {
				return errors.New("analysis execution mismatch")
			}
		}
	case "event":
		id := digest([]byte(fmt.Sprintf("%s\n%s\n%d", text(d, "job_id"), text(d, "attempt_id"), integer(d, "sequence"))))
		if id != text(d, "event_id") {
			return errors.New("event ID mismatch")
		}
		state := text(d, "state")
		terminal := state == "PASS" || state == "FAIL" || state == "WARN" || state == "ERROR"
		if terminal != (d["result"] != nil) {
			return errors.New("event result availability mismatch")
		}
		if (state == "PENDING" || state == "RUNNING") && d["analysis_id"] != nil || state == "ANALYZING" && d["analysis_id"] == nil {
			return errors.New("event analysis availability mismatch")
		}
		if terminal {
			result, err := g.reference(ctx, nested(d, "result"), "result")
			if err != nil {
				return err
			}
			r := result.doc
			if !sameJob(d, r) || text(d, "attempt_id") != text(r, "attempt_id") || state != text(r, "status") || d["analysis_id"] != r["analysis_id"] {
				return errors.New("event differs from result")
			}
		}
	case "result":
		return g.result(ctx, d)
	}
	return nil
}
func (g *graph) result(ctx context.Context, d object) error {
	job, err := g.reference(ctx, nested(d, "job"), "job")
	if err != nil {
		return err
	}
	if !sameJob(d, job.doc) || text(d, "inputs_hash") != text(job.doc, "inputs_hash") {
		return errors.New("result differs from job")
	}
	failed := text(d, "status") == "ERROR"
	if failed != (d["failure_class"] != nil) {
		return errors.New("failure class contradicts status")
	}
	if d["analysis_id"] == nil {
		if !failed || text(job.doc, "job_kind") != "run" || text(d, "failure_class") == "analysis_failure" {
			return errors.New("invalid run failure")
		}
		for _, key := range []string{"bag", "analysis_hash", "analysis_template", "metrics"} {
			if d[key] != nil {
				return errors.New("run failure requires absent analysis")
			}
		}
		return nil
	}
	bag, err := g.reference(ctx, nested(d, "bag"), "bag")
	if err != nil {
		return err
	}
	if text(bag.doc, "execution_id") != text(d, "execution_id") {
		return errors.New("result bag execution mismatch")
	}
	in := nested(job.doc, "inputs")
	if text(job.doc, "job_kind") == "run" {
		if text(bag.doc, "inputs_hash") != text(job.doc, "inputs_hash") || integer(bag.doc, "tick_ms") != integer(nested(in, "run_template"), "tick_ms") || integer(bag.doc, "record_count") > integer(nested(in, "limits"), "max_ticks")+1 {
			return errors.New("recording differs from run inputs")
		}
	} else if !reflect.DeepEqual(in["bag"], d["bag"]) {
		return errors.New("result differs from selected bag")
	}
	hash, err := hashObject(d["analysis_template"])
	if err != nil {
		return err
	}
	want, err := hashObject(in["analysis_template"])
	if err != nil {
		return err
	}
	id := digest([]byte(text(d, "execution_id") + "\n" + bag.hash + "\n" + hash))
	if hash != want || hash != text(d, "analysis_hash") || id != text(d, "analysis_id") {
		return errors.New("analysis hash or identity mismatch")
	}
	if failed {
		if d["metrics"] != nil || (text(d, "failure_class") != "worker_failure" && text(d, "failure_class") != "analysis_failure") {
			return errors.New("invalid analysis failure")
		}
		return nil
	}
	return metricsConsistent(d, integer(bag.doc, "record_count"))
}

// Readers check reported score semantics; they do not rerun metric algorithms.
func metricsConsistent(d object, records int64) error {
	metrics := nested(d, "metrics")
	if len(metrics) != 3 {
		return errors.New("completed result requires three metrics")
	}
	status := "PASS"
	for name, unit := range map[string]string{"collision_count": "count", "minimum_obstacle_gap": "mm", "goal_progress": "ppm"} {
		metric := nested(metrics, name)
		limit := nested(nested(d, "analysis_template"), name)
		if text(metric, "unit") != unit || integer(metric, "version") != integer(limit, "version") {
			return errors.New("metric version or unit mismatch")
		}
		evidence, _ := metric["evidence_ticks"].([]any)
		for _, tick := range evidence {
			value := integer(object{"tick": tick}, "tick")
			if value >= records {
				return errors.New("evidence outside recording")
			}
		}
		if metric["value"] == nil {
			if metric["pass"] != nil || len(evidence) != 0 {
				return errors.New("unavailable metric cannot pass or cite evidence")
			}
			if status != "FAIL" {
				status = "WARN"
			}
			continue
		}
		value := integer(metric, "value")
		pass := false
		switch name {
		case "collision_count":
			if value < 0 {
				return errors.New("negative collisions")
			}
			pass = value == 0
		case "minimum_obstacle_gap":
			pass = value >= integer(limit, "minimum_mm")
		case "goal_progress":
			if value < 0 || value > 1000000 {
				return errors.New("progress outside range")
			}
			pass = value >= integer(limit, "minimum_ppm")
		}
		if metric["pass"] != pass {
			return errors.New("metric pass flag contradicts score limit")
		}
		if !pass {
			status = "FAIL"
		}
	}
	if text(d, "status") != status {
		return errors.New("aggregate status contradicts metrics")
	}
	return nil
}
