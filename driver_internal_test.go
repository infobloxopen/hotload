package hotload

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"testing"
)

type testDriver struct {
	options map[string]string
}

func (d *testDriver) Open(name string) (driver.Conn, error) {
	return nil, fmt.Errorf("not implemented")
}

func withConnectionStringOptions(options map[string]string) driverOption {
	return func(di *driverInstance) {
		di.options = options
	}
}

func TestRegisterSQLDriverWithOptions(t *testing.T) {
	type args struct {
		name    string
		driver  driver.Driver
		options []driverOption
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "driver with an option",
			args: args{
				name:   "test with options",
				driver: &testDriver{},
				options: []driverOption{
					withConnectionStringOptions(map[string]string{"a": "b"}),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RegisterSQLDriver(tt.args.name, tt.args.driver, tt.args.options...)
			mu.Lock()
			defer mu.Unlock()

			d, ok := sqlDrivers[tt.args.name]
			if !ok {
				t.Errorf("RegisterSQLDriver() did not register the driver")
			}
			gotOptions := d.driver.(*testDriver).options
			if reflect.DeepEqual(gotOptions, tt.args.options) {
				t.Errorf("RegisterSQLDriver() did not set the options")
			}
		})
	}
}

func Test_mergeConnStringOptions(t *testing.T) {
	type args struct {
		dsn     string
		options map[string]string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "empty",
			args: args{
				dsn:     "",
				options: nil,
			},
			want:    "",
			wantErr: false,
		},
		{
			name: "bad dsn with no options",
			args: args{
				dsn:     "bad dsn",
				options: nil,
			},
			want:    "bad dsn",
			wantErr: false,
		},
		{
			name: "key=value dsn with options",
			args: args{
				dsn:     "host=localhost port=5432 dbname=mydb sslmode=disable",
				options: map[string]string{"application_name": "my-app"},
			},
			want:    "host=localhost port=5432 dbname=mydb sslmode=disable application_name=my-app",
			wantErr: false,
		},
		{
			name: "good dsn with no options",
			args: args{
				dsn: "postgres://localhost:5432/postgres?sslmode=disable",
			},
			want:    "postgres://localhost:5432/postgres?sslmode=disable",
			wantErr: false,
		},
		{
			name: "good dsn with options",
			args: args{
				dsn:     "postgres://localhost:5432/postgres?sslmode=disable",
				options: map[string]string{"disable_cache": "true"},
			},
			want:    "postgres://localhost:5432/postgres?disable_cache=true&sslmode=disable",
			wantErr: false,
		},
		{
			// Reproduces the CrashLoopBackOff observed in dns-config-svc pods.
			// db-controller provides DSN in key=value format and the password
			// contains URI-special characters (&, <) that break url.ParseRequestURI.
			name: "key=value dsn with special chars in password",
			args: args{
				dsn:     `host=mydb.cluster.rds.amazonaws.com port=5432 user=nstar_user password=Fhl&kB<U4eY87z. dbname=mydb sslmode=require`,
				options: map[string]string{"application_name": "dns-config-svc"},
			},
			want:    `host=mydb.cluster.rds.amazonaws.com port=5432 user=nstar_user password=Fhl&kB<U4eY87z. dbname=mydb sslmode=require application_name=dns-config-svc`,
			wantErr: false,
		},
		{
			name: "key=value dsn option value with spaces is quoted",
			args: args{
				dsn:     "host=localhost dbname=testdb",
				options: map[string]string{"application_name": "my service"},
			},
			want:    "host=localhost dbname=testdb application_name='my service'",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mergeConnStringOptions(tt.args.dsn, tt.args.options)
			if (err != nil) != tt.wantErr {
				t.Errorf("mergeConnStringOptions() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("mergeConnStringOptions() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_quoteConnStringValue(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"dns-config-svc", "dns-config-svc"},
		{"has space", "'has space'"},
		{"", "''"},
		{"has'quote", `'has\'quote'`},
		{`has\backslash`, `'has\\backslash'`},
		{"Fhl&kB<U4eY87z.", "Fhl&kB<U4eY87z."}, // URI-special chars don't need quoting in key=value
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := quoteConnStringValue(tt.input)
			if got != tt.want {
				t.Errorf("quoteConnStringValue(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
