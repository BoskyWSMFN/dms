// Package transcode implements routines for transcoding to various kinds of
// receiver.
package transcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/anacrolix/ffprobe"
	"github.com/anacrolix/log"

	. "github.com/anacrolix/dms/misc"
)

// Invokes an external command and returns a reader from its stdout. The
// command is waited on asynchronously.
func transcodePipe(ctx context.Context, args []string, stderr io.Writer) (r io.ReadCloser, err error) {
	log.Println("transcode command:", args)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Cancel = func() error {
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.Stderr = stderr
	r, err = cmd.StdoutPipe()
	if err != nil {
		return
	}
	err = cmd.Start()
	if err != nil {
		return
	}
	go func() {
		var esErr *exec.ExitError

		err := cmd.Wait()
		if err != nil {
			if errors.As(err, &esErr) && esErr.ExitCode() == 255 {
				return
			}

			log.Printf("command %s failed: %s", args, err)
		}
	}()
	return
}

// Return a series of ffmpeg arguments that pick specific codecs for specific
// streams. This requires use of the -map flag.
func generateStreamArgs(streams []map[string]interface{}) (args []string) {
	getStreamAlias := func(stream map[string]interface{}) (inputIndex int, streamAlias string) {
		indexF, ok := stream["index"].(float64)
		if !ok {
			indexJN, _ := stream["index"].(json.Number)
			indexF, _ = indexJN.Float64()
		}

		inputIndex = int(indexF)
		streamAlias = "0:" + strconv.Itoa(inputIndex)

		return
	}

	var (
		outputVideoIndex    int
		outputAudioIndex    int
		outputSubtitleIndex int
	)

	for _, stream := range streams {
		switch stream["codec_type"] {
		case "video":
			_, streamAlias := getStreamAlias(stream)

			switch stream["codec_name"] {
			case "mjpeg", "png", "bmp", "webp", "tiff", "j2k", "jpeg2000":
				args = append(args,
					"-map", streamAlias,
					"-c:v:"+strconv.Itoa(outputVideoIndex), "copy",
				)
			default:
				args = append(args,
					"-map", streamAlias,
					"-c:v:"+strconv.Itoa(outputVideoIndex), "mpeg2video",
					"-b:v:"+strconv.Itoa(outputVideoIndex), "6000k",
					"-minrate:v:"+strconv.Itoa(outputVideoIndex), "3000k",
					"-maxrate:v:"+strconv.Itoa(outputVideoIndex), "8000k",
					"-bufsize:v:"+strconv.Itoa(outputVideoIndex), "1835k",
					"-g:v:"+strconv.Itoa(outputVideoIndex), "15",
					"-bf:v:"+strconv.Itoa(outputVideoIndex), "2",
					"-flags:v:"+strconv.Itoa(outputVideoIndex), "+ilme+ildct",
					"-filter:v:"+strconv.Itoa(outputVideoIndex), "fieldorder=tff",
					"-aspect:v:"+strconv.Itoa(outputVideoIndex), "16:9",
					"-s:v:"+strconv.Itoa(outputVideoIndex), "720x576",
					"-r:v:"+strconv.Itoa(outputVideoIndex), "25",
					"-vsync", "cfr",
				)
			}

			outputVideoIndex++
		case "audio":
			_, streamAlias := getStreamAlias(stream)
			defaultFilter := true

			switch stream["codec_name"] {
			case "aac", "dca", "ac3", "mp2", "mp3", "opus", "flac", "pcm_s16le":
				args = append(args,
					"-map", streamAlias,
					"-c:a:"+strconv.Itoa(outputAudioIndex), "ac3",
					"-b:a:"+strconv.Itoa(outputAudioIndex), "224k",
					"-ac:a:"+strconv.Itoa(outputAudioIndex), "2",
				)
			case "eac3", "dts":
				args = append(args,
					"-map", streamAlias,
					"-c:a:"+strconv.Itoa(outputAudioIndex), "ac3",
					"-b:a:"+strconv.Itoa(outputAudioIndex), "448k",
					"-ac:a:"+strconv.Itoa(outputAudioIndex), "2",
				)
			case "truehd":
				args = append(args,
					"-map", streamAlias,
					"-c:a:"+strconv.Itoa(outputAudioIndex), "ac3",
					"-b:a:"+strconv.Itoa(outputAudioIndex), "640k",
					"-ac:a:"+strconv.Itoa(outputAudioIndex), "6",
					"-ar:a:"+strconv.Itoa(outputAudioIndex), "48000",
					"-sample_fmt:a:3"+strconv.Itoa(outputAudioIndex), "fltp",
					"-filter:a:"+strconv.Itoa(outputAudioIndex),
					"aresample=ocl=stereo:async=10000,"+
						"channelmap=channel_layout=5.1",
				)

				defaultFilter = false
			default:
				args = append(args,
					"-map", streamAlias,
					"-c:a:"+strconv.Itoa(outputAudioIndex), "copy",
				)
				defaultFilter = false
			}

			if defaultFilter {
				args = append(args,
					"-filter:a:"+strconv.Itoa(outputAudioIndex),
					"aresample=async=10000",
				)
			}

			outputAudioIndex++
		case "subtitle":
			_, streamAlias := getStreamAlias(stream)

			switch stream["codec_name"] {
			case "srt", "dvdsub":
				args = append(args,
					"-map", streamAlias,
					"-c:s:"+strconv.Itoa(outputSubtitleIndex), "dvbsub", "-fix_sub_duration", "-canvas_size", "720x576",
				)
			default:
				args = append(args,
					"-map", streamAlias,
					"-c:s:"+strconv.Itoa(outputSubtitleIndex), "copy",
				)
			}

			outputSubtitleIndex++
		default:
			_, streamAlias := getStreamAlias(stream)

			args = append(args,
				"-map", streamAlias,
				"-c", "copy",
			)
		}
	}

	return
}

// Streams the desired file in the MPEG_PS_PAL DLNA profile.
func Transcode(ctx context.Context, path string, start, length time.Duration, stderr io.Writer) (r io.ReadCloser, err error) {
	args := []string{
		"ffmpeg",
		"-threads", strconv.FormatInt(int64(runtime.NumCPU()), 10),
		"-ss", FormatDurationSexagesimal(start),
	}
	if length >= 0 {
		args = append(args, []string{
			"-t", FormatDurationSexagesimal(length),
		}...)
	}
	args = append(args, []string{
		"-i", path,
	}...)
	info, err := ffprobe.Run(path)
	if err != nil {
		return
	}

	args = append(args, generateStreamArgs(info.Streams)...)
	args = append(args, []string{
		"-f", "mpegts",
		"-mpegts_flags", "+resend_headers+initial_discontinuity",
		"-mpegts_service_type", "digital_tv",
		"-fflags", "+genpts+flush_packets", "-avoid_negative_ts", "make_zero",
		"-frag_duration", "1000000",
		"-final_delay", "0.7",
		"-muxdelay", "0.7", "-muxpreload", "0",
		"-flush_packets", "1",
		"pipe:"}...)
	return transcodePipe(ctx, args, stderr)
}

// Returns a stream of Chromecast supported VP8.
func VP8Transcode(ctx context.Context, path string, start, length time.Duration, stderr io.Writer) (r io.ReadCloser, err error) {
	args := []string{
		"avconv",
		"-threads", strconv.FormatInt(int64(runtime.NumCPU()), 10),
		"-async", "1",
		"-ss", FormatDurationSexagesimal(start),
	}
	if length > 0 {
		args = append(args, []string{
			"-t", FormatDurationSexagesimal(length),
		}...)
	}
	args = append(args, []string{
		"-i", path,
		// "-deadline", "good",
		// "-c:v", "libvpx", "-crf", "10",
		"-f", "webm",
		"pipe:",
	}...)
	return transcodePipe(ctx, args, stderr)
}

// Returns a stream of Chromecast supported matroska.
func ChromecastTranscode(ctx context.Context, path string, start, length time.Duration, stderr io.Writer) (r io.ReadCloser, err error) {
	args := []string{
		"ffmpeg",
		"-ss", FormatDurationSexagesimal(start),
		"-i", path,
		"-c:v", "libx264", "-preset", "fast", "-profile:v", "high", "-level", "5.0",
		"-g", "48", "-keyint_min", "48", "-sc_threshold", "0",
		"-movflags", "+faststart+frag_keyframe+default_base_moof",
		"-frag_duration", "1000000", "-min_frag_duration", "1000000",
		"-force_key_frames", "expr:gte(n,n_forced*48)",
	} // +empty_moov
	if length > 0 {
		args = append(args, []string{
			"-t", FormatDurationSexagesimal(length),
		}...)
	}
	args = append(args, []string{
		"-f", "mp4",
		"pipe:",
	}...)
	return transcodePipe(ctx, args, stderr)
}

// Returns a stream of h264 video and mp3 audio
func WebTranscode(ctx context.Context, path string, start, length time.Duration, stderr io.Writer) (r io.ReadCloser, err error) {
	args := []string{
		"ffmpeg",
		"-ss", FormatDurationSexagesimal(start),
		"-i", path,
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264", "-crf", "25",
		"-c:a", "mp3", "-ab", "128k", "-ar", "44100",
		"-preset", "ultrafast",
		"-movflags", "+faststart+frag_keyframe+empty_moov+default_base_moof",
	}
	if length > 0 {
		args = append(args, []string{
			"-t", FormatDurationSexagesimal(length),
		}...)
	}
	args = append(args, []string{
		"-f", "mp4",
		"pipe:",
	}...)
	return transcodePipe(ctx, args, stderr)
}

// credit laurent @ https://stackoverflow.com/questions/34118732/parse-a-command-line-string-into-flags-and-arguments-in-golang
func parseCommandLine(command string) ([]string, error) {
	var args []string
	state := "start"
	current := ""
	quote := "\""
	escapeNext := true
	for i := 0; i < len(command); i++ {
		c := command[i]

		if state == "quotes" {
			if string(c) != quote {
				current += string(c)
			} else {
				args = append(args, current)
				current = ""
				state = "start"
			}
			continue
		}

		if escapeNext {
			current += string(c)
			escapeNext = false
			continue
		}

		if c == '\\' {
			escapeNext = true
			continue
		}

		if c == '"' || c == '\'' {
			state = "quotes"
			quote = string(c)
			continue
		}

		if state == "arg" {
			if c == ' ' || c == '\t' {
				args = append(args, current)
				current = ""
				state = "start"
			} else {
				current += string(c)
			}
			continue
		}

		if c != ' ' && c != '\t' {
			state = "arg"
			current += string(c)
		}
	}

	if state == "quotes" {
		return []string{}, fmt.Errorf("Unclosed quote in command line: %s", command)
	}

	if current != "" {
		args = append(args, current)
	}

	return args, nil
}

// Exec runs the cmd to generate the video to stream. It does not support seeking. Used by the dynamic stream feature.
func Exec(ctx context.Context, cmds string, start, length time.Duration, stderr io.Writer) (r io.ReadCloser, err error) {
	cmda, aerr := parseCommandLine(cmds)
	if aerr != nil {
		err = aerr
		return
	}
	return transcodePipe(ctx, cmda, stderr)
}
