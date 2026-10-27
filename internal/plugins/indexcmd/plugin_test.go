package indexcmd

import (
	"reflect"
	"testing"
)

func TestParseIndexArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    indexOptions
		wantErr bool
	}{
		{
			name: "single file",
			raw:  "document.pdf",
			want: indexOptions{
				target: "document.pdf",
			},
		},
		{
			name: "directory with short recursive flag",
			raw:  "docs/ -r",
			want: indexOptions{
				target:    "docs/",
				recursive: true,
			},
		},
		{
			name: "directory with long recursive flag and quoted message",
			raw:  `project/ --recursive -m "API documentation"`,
			want: indexOptions{
				target:    "project/",
				message:   "API documentation",
				recursive: true,
			},
		},
		{
			name: "directory with exclude list",
			raw:  "repo/ -r --exclude .git,node_modules,dist",
			want: indexOptions{
				target:    "repo/",
				recursive: true,
				excludes:  []string{".git", "node_modules", "dist"},
			},
		},
		{
			name: "directory with equals exclude flag",
			raw:  "repo/ -r --exclude=vendor,build",
			want: indexOptions{
				target:    "repo/",
				recursive: true,
				excludes:  []string{"vendor", "build"},
			},
		},
		{
			name:    "empty args",
			raw:     "   ",
			wantErr: true,
		},
		{
			name:    "missing message value",
			raw:     "docs/ -r -m",
			wantErr: true,
		},
		{
			name:    "missing exclude value",
			raw:     "docs/ -r --exclude",
			wantErr: true,
		},
		{
			name:    "unterminated quote",
			raw:     `docs/ -r -m "unterminated`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseIndexArgs(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseIndexArgs(%q) expected error, got nil", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseIndexArgs(%q) unexpected error: %v", tt.raw, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseIndexArgs(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}
