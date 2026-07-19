package task

import "fmt"

type ErrorCode string

const (
	ErrInvalidTaskSchema      ErrorCode = "INVALID_TASK_SCHEMA"
	ErrInputNotFound          ErrorCode = "INPUT_NOT_FOUND"
	ErrInputNotReadable       ErrorCode = "INPUT_NOT_READABLE"
	ErrDownloadFailed         ErrorCode = "DOWNLOAD_FAILED"
	ErrProbeFailed            ErrorCode = "PROBE_FAILED"
	ErrUnsupportedMedia       ErrorCode = "UNSUPPORTED_MEDIA"
	ErrUnsupportedDolbyVision ErrorCode = "UNSUPPORTED_DOLBY_VISION"
	ErrNoValidVideoProfile    ErrorCode = "NO_VALID_VIDEO_PROFILE"
	ErrVideoTranscodeFailed   ErrorCode = "VIDEO_TRANSCODE_FAILED"
	ErrAudioTranscodeFailed   ErrorCode = "AUDIO_TRANSCODE_FAILED"
	ErrNoPlayableAudio        ErrorCode = "NO_PLAYABLE_AUDIO"
	ErrSubtitleConvertFailed  ErrorCode = "SUBTITLE_CONVERT_FAILED"
	ErrSpriteGenerationFailed ErrorCode = "SPRITE_GENERATION_FAILED"
	ErrPackagingFailed        ErrorCode = "PACKAGING_FAILED"
	ErrOutputValidationFailed ErrorCode = "OUTPUT_VALIDATION_FAILED"
	ErrTaskOutputConflict     ErrorCode = "TASK_OUTPUT_CONFLICT"
)

type Error struct {
	Code      ErrorCode      `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Step      string         `json:"step,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Step != "" {
		return fmt.Sprintf("%s (%s): %s", e.Code, e.Step, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewError(code ErrorCode, message string, retryable bool) *Error {
	return &Error{Code: code, Message: message, Retryable: retryable}
}
