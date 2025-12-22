package devwatch

import (
	"testing"
)

func TestContain(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		setup    func() *DevWatch
		expected bool
	}{
		{
			name: "hidden file",
			path: ".gitignore",
			setup: func() *DevWatch {
				return &DevWatch{
					WatchConfig: &WatchConfig{
						UnobservedFiles: func() []string {
							return []string{}
						},
					},
				}
			},
			expected: true,
		},
		{
			name: "unobserved file",
			path: "test/.git",
			setup: func() *DevWatch {
				return &DevWatch{
					WatchConfig: &WatchConfig{
						UnobservedFiles: func() []string {
							return []string{".git"}
						},
					},
				}
			},
			expected: true,
		},
		{
			name: "observed file",
			path: "test/main.go",
			setup: func() *DevWatch {
				return &DevWatch{
					WatchConfig: &WatchConfig{
						UnobservedFiles: func() []string {
							return []string{".git"}
						},
					},
				}
			},
			expected: false,
		},
		{
			name: "git folder in middle of path",
			path: "C:\\Users\\Cesar\\Packages\\Internal\\godev\\test\\manual\\.git\\objects\\pack",
			setup: func() *DevWatch {
				return &DevWatch{
					WatchConfig: &WatchConfig{
						UnobservedFiles: func() []string {
							return []string{".git"}
						},
					},
				}
			},
			expected: true,
		},
		{
			name: "git folder in middle of path with unix style",
			path: "/Users/Cesar/Packages/Internal/godev/test/manual/.git/objects/pack",
			setup: func() *DevWatch {
				return &DevWatch{
					WatchConfig: &WatchConfig{
						UnobservedFiles: func() []string {
							return []string{".git"}
						},
					},
				}
			},
			expected: true,
		},
		{
			name: "git folder in middle of path with project root",
			path: "test/manual/.git/objects/pack",
			setup: func() *DevWatch {
				return &DevWatch{
					WatchConfig: &WatchConfig{
						UnobservedFiles: func() []string {
							return []string{".git"}
						},
					},
				}
			},
			expected: true,
		},
		{
			name: "git folder in middle of path with absolute unix path",
			path: "/home/user/Dev/Pkg/Mine/godev/test/manual/.git/objects/dc",
			setup: func() *DevWatch {
				return &DevWatch{
					WatchConfig: &WatchConfig{
						UnobservedFiles: func() []string {
							return []string{".git"}
						},
					},
				}
			},
			expected: true,
		},
		{
			name: "git string in directory name but not excluded",
			path: "test/github-integration/code.go",
			setup: func() *DevWatch {
				return &DevWatch{
					WatchConfig: &WatchConfig{
						UnobservedFiles: func() []string {
							return []string{".git"}
						},
					},
				}
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := tt.setup()
			if got := handler.Contain(tt.path); got != tt.expected {
				t.Errorf("Contain(%q) = %v; want %v", tt.path, got, tt.expected)
			}
		})
	}
}
