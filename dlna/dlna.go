package dlna

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

const (
	TimeSeekRangeDomain   = "TimeSeekRange.dlna.org"
	ContentFeaturesDomain = "contentFeatures.dlna.org"
	TransferModeDomain    = "transferMode.dlna.org"
)

type ContentFeatures struct {
	ProfileName     string
	SupportTimeSeek bool
	SupportRange    bool
	// Play speeds, DLNA.ORG_PS would go here if supported.
	Transcoded bool
	// DLNA.ORG_FLAGS go here if you need to tweak.
	Flags string
}

func BinaryInt(b bool) uint {
	if b {
		return 1
	} else {
		return 0
	}
}

// flags are in hex. trailing 24 zeroes, 26 are after the space
// "DLNA.ORG_OP=" time-seek-range-supp bytes-range-header-supp
func (cf ContentFeatures) String() (ret string) {
	// DLNA.ORG_PN=[a-zA-Z0-9_]*
	params := make([]string, 0, 3)
	if cf.ProfileName != "" {
		params = append(params, "DLNA.ORG_PN="+cf.ProfileName)
	}
	params = append(params, fmt.Sprintf(
		"DLNA.ORG_OP=%b%b;DLNA.ORG_CI=%b",
		BinaryInt(cf.SupportTimeSeek),
		BinaryInt(cf.SupportRange),
		BinaryInt(cf.Transcoded)))
	// https://stackoverflow.com/questions/29182754/c-dlna-generate-dlna-org-flags
	// DLNA_ORG_FLAG_STREAMING_TRANSFER_MODE | DLNA_ORG_FLAG_BACKGROUND_TRANSFERT_MODE | DLNA_ORG_FLAG_CONNECTION_STALL | DLNA_ORG_FLAG_DLNA_V15
	flags := "01700000000000000000000000000000"
	if cf.Flags != "" {
		flags = cf.Flags
	}
	params = append(params, "DLNA.ORG_FLAGS="+flags)
	return strings.Join(params, ";")
}

func ParseNPTTime(s string) (time.Duration, error) {
	var h, m, sec, ms time.Duration
	n, err := fmt.Sscanf(s, "%d:%2d:%2d.%3d", &h, &m, &sec, &ms)
	if err != nil {
		return -1, err
	}
	if n < 3 {
		return -1, fmt.Errorf("invalid npt time: %s", s)
	}
	ret := time.Duration(h) * time.Hour
	ret += time.Duration(m) * time.Minute
	ret += sec * time.Second
	ret += ms * time.Millisecond
	return ret, nil
}

func FormatNPTTime(npt time.Duration) string {
	npt /= time.Millisecond
	ms := npt % 1000
	npt /= 1000
	s := npt % 60
	npt /= 60
	m := npt % 60
	npt /= 60
	h := npt
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

type NPTRange struct {
	Start     time.Duration // NPT start time
	End       time.Duration // NPT end time
	StartByte int64         // Byte offset start
	EndByte   int64         // Byte offset end (inclusive)
}

func ParseNPTRange(s string) (ret NPTRange, err error) {
	ss := strings.SplitN(s, "-", 2)
	if ss[0] != "" {
		ret.Start, err = ParseNPTTime(ss[0])
		if err != nil {
			return
		}
	}
	if ss[1] != "" {
		ret.End, err = ParseNPTTime(ss[1])
		if err != nil {
			return
		}
	}
	return
}

// calculateNPTPosition calculates the NPT position with high precision
func calculateNPTPosition(bytePos, totalSize int64, duration time.Duration) time.Duration {
	if totalSize <= 0 {
		return 0
	}

	position := new(big.Rat).SetFrac64(bytePos, totalSize)
	nanos := new(big.Rat).Mul(position, new(big.Rat).SetInt64(int64(duration)))

	durNanos, _ := nanos.Float64()
	return time.Duration(math.Round(durNanos))
}

func ParseHTTPRangeToNPTRange(rangeHeader string, totalSize int64, duration time.Duration) (NPTRange, error) {
	const prefix = "bytes="

	if rangeHeader == prefix+"0-" || rangeHeader == prefix+"0-"+strconv.FormatInt(totalSize-1, 10) {
		return NPTRange{
			End:     duration,
			EndByte: totalSize - 1,
		}, nil
	}

	if !strings.HasPrefix(rangeHeader, prefix) {
		return NPTRange{}, fmt.Errorf("unsupported range format: %q", rangeHeader)
	}

	rangeSpec := strings.TrimPrefix(rangeHeader, prefix)
	parts := strings.SplitN(rangeSpec, "-", 2)
	if len(parts) != 2 {
		return NPTRange{}, fmt.Errorf("invalid range format")
	}

	startByte, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return NPTRange{}, fmt.Errorf("invalid start byte value")
	}

	endByte := totalSize - 1
	if parts[1] != "" {
		endByte, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return NPTRange{}, fmt.Errorf("invalid end byte value")
		}
	}

	if startByte < 0 || endByte >= totalSize || startByte > endByte {
		return NPTRange{}, fmt.Errorf("range out of bounds")
	}

	startTime := calculateNPTPosition(startByte, totalSize, duration)
	endTime := calculateNPTPosition(endByte+1, totalSize, duration)

	if endTime > duration {
		endTime = duration
	}

	return NPTRange{
		Start:     startTime,
		End:       endTime,
		StartByte: startByte,
		EndByte:   endByte,
	}, nil
}

func (me NPTRange) String() (ret string) {
	ret = me.Start.String() + "-"
	if me.End >= 0 {
		ret += me.End.String()
	}
	return
}
