package remotefs

import "testing"

func TestIsNotFoundRecognizesCommonRemoteErrorShapes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "entry not found code",
			err:  &RemoteError{Code: "EntryNotFound", Message: "missing"},
			want: true,
		},
		{
			name: "file not found code",
			err:  &RemoteError{Code: "FileNotFound", Message: "missing"},
			want: true,
		},
		{
			name: "not found message",
			err:  &RemoteError{Code: "UnknownError", Message: "File not found"},
			want: true,
		},
		{
			name: "other remote error",
			err:  &RemoteError{Code: "PermissionDenied", Message: "denied"},
			want: false,
		},
	}

	for _, tc := range tests {
		if got := IsNotFound(tc.err); got != tc.want {
			t.Fatalf("%s: IsNotFound() = %v, want %v", tc.name, got, tc.want)
		}
	}
}
