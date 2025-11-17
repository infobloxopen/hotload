package dsnutil

import "testing"

func TestIsCredentialOnlyChange(t *testing.T) {
	tests := []struct {
		name   string
		oldDSN string
		newDSN string
		want   bool
	}{
		{
			name:   "password changed",
			oldDSN: "postgresql://user:pass1@localhost:5432/mydb?sslmode=disable",
			newDSN: "postgresql://user:pass2@localhost:5432/mydb?sslmode=disable",
			want:   true,
		},
		{
			name:   "username changed",
			oldDSN: "postgresql://user1:pass@localhost:5432/mydb",
			newDSN: "postgresql://user2:pass@localhost:5432/mydb",
			want:   true,
		},
		{
			name:   "host changed",
			oldDSN: "postgresql://user:pass@localhost:5432/mydb",
			newDSN: "postgresql://user:pass@remotehost:5432/mydb",
			want:   false,
		},
		{
			name:   "port changed",
			oldDSN: "postgresql://user:pass@localhost:5432/mydb",
			newDSN: "postgresql://user:pass@localhost:5433/mydb",
			want:   false,
		},
		{
			name:   "database changed",
			oldDSN: "postgresql://user:pass@localhost:5432/mydb",
			newDSN: "postgresql://user:pass@localhost:5432/otherdb",
			want:   false,
		},
		{
			name:   "option added",
			oldDSN: "postgresql://user:pass@localhost:5432/mydb",
			newDSN: "postgresql://user:pass@localhost:5432/mydb?sslmode=disable",
			want:   false,
		},
		{
			name:   "no change",
			oldDSN: "postgresql://user:pass@localhost:5432/mydb",
			newDSN: "postgresql://user:pass@localhost:5432/mydb",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsCredentialOnlyChange(tt.oldDSN, tt.newDSN)
			if got != tt.want {
				t.Errorf("IsCredentialOnlyChange() = %v, want %v", got, tt.want)
			}
		})
	}
}
