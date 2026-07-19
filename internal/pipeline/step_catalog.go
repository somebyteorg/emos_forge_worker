package pipeline

import (
	"strings"
)

type stepDescriptor struct {
	ExternalName string
}

var stepCatalog = map[string]stepDescriptor{
	StepPrepare:         {ExternalName: "prepare"},
	StepDownloadURL:     {ExternalName: "download_url"},
	StepProbe:           {ExternalName: "probe"},
	StepValidateInput:   {ExternalName: "validate_input"},
	StepSubtitlePackage: {ExternalName: "subtitle_package"},
	StepVideoPackage:    {ExternalName: "video_package"},
	StepVideoGenerate:   {ExternalName: "video_generate"},
	StepAudioSelect:     {ExternalName: "audio_select"},
	StepAudioAAC:        {ExternalName: "audio_aac"},
	StepAudioPackage:    {ExternalName: "audio_package"},
	StepSpritesGenerate: {ExternalName: "sprites_generate"},
	StepValidateOutput:  {ExternalName: "validate_output"},
	StepFinalize:        {ExternalName: "finalize"},
}

func ExternalStepName(name string) string {
	if descriptor, ok := stepCatalog[name]; ok {
		return descriptor.ExternalName
	}
	return strings.ReplaceAll(name, ".", "_")
}
