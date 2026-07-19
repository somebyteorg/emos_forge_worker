package emos

import "testing"

func TestReadableResponseTextDecodesChinese(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "json object", in: `{"message":"\u4efb\u52a1\u4e0d\u5b58\u5728"}`, want: `{"message":"任务不存在"}`},
		{name: "json string", in: `"\u4efb\u52a1\u4e0d\u5b58\u5728"`, want: "任务不存在"},
		{name: "escaped text", in: `error: \u4efb\u52a1\u4e0d\u5b58\u5728`, want: "error: 任务不存在"},
		{name: "percent encoded", in: `%E4%BB%BB%E5%8A%A1%E4%B8%8D%E5%AD%98%E5%9C%A8`, want: "任务不存在"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readableResponseText(tt.in); got != tt.want {
				t.Fatalf("readableResponseText() = %q, want %q", got, tt.want)
			}
		})
	}
}
