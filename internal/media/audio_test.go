package media

import "testing"

func TestSelectAudioTracksCopyAllKeepsSourceCodec(t *testing.T) {
	selections, err := SelectAudioTracks([]AudioTrack{
		{Index: 1, Codec: "eac3", Language: "ENG", Channels: 6, ChannelLayout: "5.1", Bitrate: 640_000, Default: true},
	}, AudioSelectionOptions{Strategy: "one_per_language", MaxChannels: 2, CopyAll: true})
	if err != nil {
		t.Fatalf("SelectAudioTracks: %v", err)
	}
	if len(selections) != 1 {
		t.Fatalf("selections = %d", len(selections))
	}
	selection := selections[0]
	if !selection.Copy || selection.OutputCodec != "eac3" || selection.OutputChannels != 6 || selection.OutputBitrate != 640_000 {
		t.Fatalf("source audio was not preserved: %+v", selection)
	}
}

func TestNewAACAudioSelectionUsesChannelAwareBitrate(t *testing.T) {
	selection := NewAACAudioSelection(AudioTrack{Index: 1, Codec: "eac3", Language: "eng", Channels: 6, Bitrate: 768_000}, 6)
	if selection.Copy || selection.OutputCodec != "aac" || selection.OutputProfile != "lc" || selection.OutputChannels != 6 || selection.OutputBitrate != 384_000 {
		t.Fatalf("unexpected 5.1 AAC selection: %+v", selection)
	}

	stereo := NewAACAudioSelection(AudioTrack{Index: 2, Codec: "eac3", Language: "eng", Channels: 6}, 2)
	if stereo.OutputChannels != 2 || stereo.OutputBitrate != 128_000 {
		t.Fatalf("unexpected stereo AAC selection: %+v", stereo)
	}
}

func TestCanCopyAudioToHLS(t *testing.T) {
	for _, codec := range []string{"aac", "ac3", "eac3"} {
		if !CanCopyAudioToHLS(codec) {
			t.Fatalf("codec %s should be copied to HLS", codec)
		}
	}
	for _, codec := range []string{"truehd", "dts", "flac", "opus"} {
		if CanCopyAudioToHLS(codec) {
			t.Fatalf("codec %s should be transcoded for HLS", codec)
		}
	}
}
