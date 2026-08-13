package benchmarkexternal

import (
	"fmt"
	"reflect"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkdrivers"
)

// canonicalExecutionDriver deliberately duplicates the complete records
// for which this package owns executable argv. A merely schema-valid retained
// lock is not enough: verification must reject a coherently redigested commit,
// tag, parser, repository, or workload expansion that this adapter did not
// implement and test.
func canonicalExecutionDriver(id string) (benchmarkdrivers.Driver, error) {
	switch id {
	case "benchbase-postgresql-33c0047":
		return benchmarkdrivers.Driver{
			ID: "benchbase-postgresql-33c0047", Adapter: "benchbase",
			DisplayVersion: "2023-SNAPSHOT+33c0047", Repository: "https://github.com/cmu-db/benchbase",
			RefType: "commit", Ref: "33c00473807ebd49304d114a6d769d2d2b2bbb34",
			Commit: "33c00473807ebd49304d114a6d769d2d2b2bbb34", Entrypoint: "java -jar benchbase.jar",
			ResultFormat: "benchbase-summary-json@33c0047", Parser: "benchbase-summary/33c0047-v1",
			RuntimeSupport: []string{"native"}, Workloads: []string{"smallbank", "tatp", "tpcc", "tpch", "ycsb"},
			BinaryDistributedByProject: false, SourceToBinaryAttested: false, DecisionEligible: false,
		}, nil
	case "sysbench-postgresql-1.0.20":
		return benchmarkdrivers.Driver{
			ID: "sysbench-postgresql-1.0.20", Adapter: "sysbench1", DisplayVersion: "1.0.20",
			Repository: "https://github.com/akopytov/sysbench", RefType: "tag", Ref: "1.0.20",
			TagObject: "f3da4313f8177d072b7150be5d00e4adfd15945c", Commit: "ebf1c90da05dea94648165e4f149abc20c979557",
			Entrypoint: "sysbench", ResultFormat: "sysbench-1.0-console-summary", Parser: "sysbench-console-summary/v1.1",
			RuntimeSupport:             []string{"native"},
			Workloads:                  []string{"oltp_point_select/postgresql", "oltp_read_only/postgresql", "oltp_read_write/postgresql", "oltp_write_only/postgresql"},
			BinaryDistributedByProject: false, SourceToBinaryAttested: false, DecisionEligible: false,
		}, nil
	case "hammerdb-postgresql-6.0":
		return benchmarkdrivers.Driver{
			ID: "hammerdb-postgresql-6.0", Adapter: "hammerdb6", DisplayVersion: "6.0",
			Repository: "https://github.com/TPC-Council/HammerDB", RefType: "tag", Ref: "v6.0",
			TagObject: "18f3e075f4d94fa1dcc3b9f11e743928ef0f7694", Commit: "d33f879aec858063edd17aa2daa46db03abb2bae",
			Entrypoint: "hammerdbcli auto", ResultFormat: "hammerdb-job-report-v1", Parser: "hammerdb6-job-report/v1",
			RuntimeSupport:             []string{"native"},
			Workloads:                  []string{"tprocc/postgresql", "tproch/postgresql"},
			BinaryDistributedByProject: false, SourceToBinaryAttested: false, DecisionEligible: false,
		}, nil
	default:
		return benchmarkdrivers.Driver{}, fmt.Errorf("driver %q has no implemented fixed execution adapter", id)
	}
}

func validateExecutionDriver(driver benchmarkdrivers.Driver) error {
	want, err := canonicalExecutionDriver(driver.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(driver, want) {
		return fmt.Errorf("driver %q does not exactly match the execution adapter pin", driver.ID)
	}
	return nil
}
