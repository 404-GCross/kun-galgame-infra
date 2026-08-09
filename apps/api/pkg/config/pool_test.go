// pool_test.go — every database in the process must be bounded.
//
// On 2026-08-09 the fleet ran on database/sql's defaults: unlimited open
// connections and two idle ones. One service took 66 of postgres' 100 slots and
// everything else — the other services, the deploy's migrate job, psql itself —
// spent an hour and a half being told "sorry, too many clients already".
//
// The bound is applied in one loop in Load(), which fixes the databases that
// exist today and does nothing for the one somebody adds next month. So the
// test below does not enumerate them: it walks Config by reflection and fails
// on any DatabaseConfig that Load left unbounded, which is the only version of
// this test that still works after the code it guards has been forgotten.
package config

import (
	"reflect"
	"testing"
)

func TestEveryDatabaseConfigIsBounded(t *testing.T) {
	// Load validates the secrets it cannot invent; supply the minimum so the
	// pool assertions below are what fails, if anything does.
	t.Setenv("KUN_PG_PASSWORD", "test")
	t.Setenv("JWT_SECRET", "test")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	v := reflect.ValueOf(*cfg)
	dbType := reflect.TypeFor[DatabaseConfig]()
	seen := 0
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if f.Type != dbType {
			continue
		}
		seen++
		pool := v.Field(i).Interface().(DatabaseConfig).Pool
		if pool.MaxOpen <= 0 {
			t.Errorf("%s: MaxOpen is %d — an unbounded pool can take every slot on the server",
				f.Name, pool.MaxOpen)
		}
		if pool.MaxIdle <= 0 {
			t.Errorf("%s: MaxIdle is %d — connections closed on release churn through TIME_WAIT",
				f.Name, pool.MaxIdle)
		}
		if pool.MaxLifetime <= 0 || pool.MaxIdleTime <= 0 {
			t.Errorf("%s: lifetime=%v idle=%v — both must be finite so slots come back",
				f.Name, pool.MaxLifetime, pool.MaxIdleTime)
		}
	}
	if seen == 0 {
		t.Fatal("no DatabaseConfig fields found — the walk broke, not the config")
	}
}

// TestPoolBoundsAreOverridable: the numbers are a default, not a law. An
// operator draining a saturated server has to be able to shrink them without a
// rebuild.
func TestPoolBoundsAreOverridable(t *testing.T) {
	t.Setenv("KUN_PG_MAX_OPEN_CONNS", "3")
	t.Setenv("KUN_PG_MAX_IDLE_CONNS", "1")
	t.Setenv("KUN_PG_CONN_MAX_LIFETIME_SECONDS", "60")
	t.Setenv("KUN_PG_CONN_MAX_IDLE_SECONDS", "30")

	got := loadPoolConfig()
	if got.MaxOpen != 3 || got.MaxIdle != 1 {
		t.Fatalf("env ignored: %+v", got)
	}
	if got.MaxLifetime.Seconds() != 60 || got.MaxIdleTime.Seconds() != 30 {
		t.Fatalf("durations ignored: %+v", got)
	}
}
