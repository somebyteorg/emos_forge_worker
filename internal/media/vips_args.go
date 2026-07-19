package media

import (
	"fmt"
	"strconv"
	"strings"
)

type VipsJoinSpec struct {
	Inputs  []string
	Output  string
	Columns int
	Quality int
	Effort  int
}

type VipsResizeSpec struct {
	Input   string
	Output  string
	Scale   float64
	Quality int
	Effort  int
}

func BuildVipsArrayJoinArgs(spec VipsJoinSpec) ([]string, error) {
	if len(spec.Inputs) == 0 || spec.Output == "" || spec.Columns <= 0 {
		return nil, fmt.Errorf("vips join inputs, output, and columns are required")
	}
	quality := spec.Quality
	if quality <= 0 {
		quality = 70
	}
	effort := spec.Effort
	if effort < 0 {
		effort = 4
	}
	if quality > 100 || effort > 9 {
		return nil, fmt.Errorf("vips AVIF quality or effort is outside supported range")
	}
	output := fmt.Sprintf("%s[Q=%d,effort=%d]", spec.Output, quality, effort)
	args := []string{
		"arrayjoin",
		strings.Join(spec.Inputs, " "),
		output,
		"--across", strconv.Itoa(spec.Columns),
		"--vips-progress",
	}
	return args, nil
}

func BuildVipsResizeArgs(spec VipsResizeSpec) ([]string, error) {
	if spec.Input == "" || spec.Output == "" || spec.Scale <= 0 {
		return nil, fmt.Errorf("vips resize input, output, and scale are required")
	}
	quality := spec.Quality
	if quality <= 0 {
		quality = 70
	}
	effort := spec.Effort
	if effort < 0 {
		effort = 4
	}
	if quality > 100 || effort > 9 {
		return nil, fmt.Errorf("vips AVIF quality or effort is outside supported range")
	}
	output := fmt.Sprintf("%s[Q=%d,effort=%d]", spec.Output, quality, effort)
	return []string{"resize", spec.Input, output, strconv.FormatFloat(spec.Scale, 'f', -1, 64), "--vips-progress"}, nil
}
