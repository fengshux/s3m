package commands

import "testing"

func TestParseBucketPath(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		wantBucket string
		wantPath   string
		wantErr    bool
	}{
		{
			name:       "普通路径",
			target:     "my-bucket/file.txt",
			wantBucket: "my-bucket",
			wantPath:   "file.txt",
		},
		{
			name:       "对象名含冒号",
			target:     "my-bucket/a:b.txt",
			wantBucket: "my-bucket",
			wantPath:   "a:b.txt",
		},
		{
			name:       "多级路径",
			target:     "my-bucket/photos/2024/img.jpg",
			wantBucket: "my-bucket",
			wantPath:   "photos/2024/img.jpg",
		},
		{
			name:       "目录前缀",
			target:     "my-bucket/photos/",
			wantBucket: "my-bucket",
			wantPath:   "photos/",
		},
		{
			name:       "仅 bucket，无 path",
			target:     "my-bucket",
			wantBucket: "my-bucket",
			wantPath:   "",
		},
		{
			name:       "bucket 带斜杠但 path 为空",
			target:     "my-bucket/",
			wantBucket: "my-bucket",
			wantPath:   "",
		},
		{
			name:    "bucket 为空",
			target:  "/file.txt",
			wantErr: true,
		},
		{
			name:    "空字符串",
			target:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBucket, gotPath, err := parseBucketPath(tt.target)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseBucketPath(%q) 期望报错，实际返回 bucket=%q path=%q",
						tt.target, gotBucket, gotPath)
				}
				return
			}

			if err != nil {
				t.Fatalf("parseBucketPath(%q) 意外报错: %v", tt.target, err)
			}
			if gotBucket != tt.wantBucket {
				t.Errorf("parseBucketPath(%q) bucket = %q, 期望 %q", tt.target, gotBucket, tt.wantBucket)
			}
			if gotPath != tt.wantPath {
				t.Errorf("parseBucketPath(%q) path = %q, 期望 %q", tt.target, gotPath, tt.wantPath)
			}
		})
	}
}
