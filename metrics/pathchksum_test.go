package metrics

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/infobloxopen/hotload/internal"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var delayDur = 616789 * time.Microsecond

func MyTestFileHasher(filePath string) (uint64, error) {
	return uint64(time.Now().UnixMicro()), nil
}

func TestPathChksumMetricsDisabled(t *testing.T) {
	os.Unsetenv(PathChksumMetricsEnableEnvVar)
	pthmDisabled := newPathChksum(MyTestFileHasher)

	time.Sleep(delayDur)
	err := testutil.CollectAndCompare(HotloadPathChksumTimestampSecondsGaugeFuncVec,
		strings.NewReader(""))
	if err != nil {
		t.Errorf("CollectAndCompare (zero paths) err=%v", err)
	}

	time.Sleep(delayDur)
	rwDsn := "/env/unset/db-dsn/dsn.txt"
	err = pthmDisabled.addPath(rwDsn)
	if err != nil {
		t.Errorf("addPath(%s) err=%v", rwDsn, err)
	}
	err = testutil.CollectAndCompare(HotloadPathChksumTimestampSecondsGaugeFuncVec,
		strings.NewReader(""))
	if err != nil {
		t.Errorf("CollectAndCompare (one path) err=%v", err)
	}

	time.Sleep(delayDur)
	roDsn := "/env/unset/db-dsn/ro-dsn.txt"
	err = pthmDisabled.addPath("/env/unset/db-dsn/ro-dsn.txt")
	if err != nil {
		t.Errorf("addPath(%s) err=%v", roDsn, err)
	}
	err = testutil.CollectAndCompare(HotloadPathChksumTimestampSecondsGaugeFuncVec,
		strings.NewReader(""))
	if err != nil {
		t.Errorf("CollectAndCompare (two paths) err=%v", err)
	}
}

func TestPathChksumMetricsEnabled(t *testing.T) {
	os.Setenv(PathChksumMetricsEnableEnvVar, "true")
	pthmEnabled := newPathChksum(MyTestFileHasher)

	time.Sleep(delayDur)
	err := testutil.CollectAndCompare(HotloadPathChksumTimestampSecondsGaugeFuncVec,
		strings.NewReader(""))
	if err != nil {
		t.Errorf("CollectAndCompare (zero paths) err=%v", err)
	}

	time.Sleep(delayDur)
	rwDsn := "/env/true/db-dsn/dsn.txt"
	err = pthmEnabled.addPath(rwDsn)
	if err != nil {
		t.Errorf("addPath(%s) err=%v", rwDsn, err)
	}
	err = internal.CollectAndRegexpCompare(HotloadPathChksumTimestampSecondsGaugeFuncVec,
		strings.NewReader(ExpectHotloadPathChksumTimestampSecondsPreamble+
			fmt.Sprintf(ExpectHotloadPathChksumTimestampSecondsRegexp, rwDsn)),
		HotloadPathChksumTimestampSecondsName)
	if err != nil {
		t.Errorf("CollectAndRegexpCompare (one path) err=%v", err)
	}

	time.Sleep(delayDur)
	roDsn := "/env/true/db-dsn/ro-dsn.txt"
	err = pthmEnabled.addPath(roDsn)
	if err != nil {
		t.Errorf("addPath(%s) err=%v", roDsn, err)
	}
	err = internal.CollectAndRegexpCompare(HotloadPathChksumTimestampSecondsGaugeFuncVec,
		strings.NewReader(ExpectHotloadPathChksumTimestampSecondsPreamble+
			fmt.Sprintf(ExpectHotloadPathChksumTimestampSecondsRegexp, rwDsn)+
			fmt.Sprintf(ExpectHotloadPathChksumTimestampSecondsRegexp, roDsn)),
		HotloadPathChksumTimestampSecondsName)
	if err != nil {
		t.Errorf("CollectAndRegexpCompare (two paths) err=%v", err)
	}

	err = pthmEnabled.addPath(rwDsn)
	if err != ErrDuplicatePath {
		t.Errorf("addPath(%s): expecting ErrDuplicatePath, but got err=%v", rwDsn, err)
	}
}
