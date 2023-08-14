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

func Test_mergeConnectionStringOptions(t *testing.T) {
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
			name: "bad dsn with options",
			args: args{
				dsn:     "bad dsn",
				options: map[string]string{"a": "b"},
			},
			want:    "",
			wantErr: true,
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mergeConnectionStringOptions(tt.args.dsn, tt.args.options)
			if (err != nil) != tt.wantErr {
				t.Errorf("mergeConnectionStringOptions() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("mergeConnectionStringOptions() = %v, want %v", got, tt.want)
			}
		})
	}
}
