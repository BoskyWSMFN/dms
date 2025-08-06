package misc

import (
	"fmt"
	"strings"
	"time"
)

func FormatDurationSexagesimal(d time.Duration) string {
	ns := d % time.Second
	d /= time.Second
	s := d % 60
	d /= 60
	m := d % 60
	d /= 60
	h := d
	ret := fmt.Sprintf("%d:%02d:%02d.%09d", h, m, s, ns)
	ret = strings.TrimRight(ret, "0")
	ret = strings.TrimRight(ret, ".")
	return ret
}

//func FormatDurationSexagesimal(d time.Duration) string {
//	return fmt.Sprintf("%02d:%02d:%06.3f",
//		int(d.Hours()), int(d.Minutes())%60, float64(d.Seconds())-float64(int(d.Minutes())*60))
//}
