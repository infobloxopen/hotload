package hotload

import (
	"testing"
)

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
