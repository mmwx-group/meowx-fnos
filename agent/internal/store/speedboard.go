package store

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sync"
)

// SpeedBoard：网页「网络测速」上传到 MeowX 测速榜的本机设置（/data/speedboard.json，规格 miaomiaowuC/Design/speedtest-board.md §6，
// 同客户端）：用户同意与否（没问过时不写）与本机随机身份 install_id（UUID v4，「重置身份」换一个）。
type SpeedBoard struct {
	mu   sync.Mutex
	path string
}

type SpeedBoardState struct {
	Upload    *bool  `json:"upload,omitempty"`
	InstallID string `json:"install_id"`
}

var installIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func OpenSpeedBoard(path string) *SpeedBoard { return &SpeedBoard{path: path} }

func newInstallID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// change 改完存盘；install_id 缺了或不合法顺手补一个
func (b *SpeedBoard) change(fn func(st *SpeedBoardState) bool) (SpeedBoardState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var st SpeedBoardState
	if data, err := os.ReadFile(b.path); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	changed := fn != nil && fn(&st)
	if !installIDPattern.MatchString(st.InstallID) {
		id, err := newInstallID()
		if err != nil {
			return st, err
		}
		st.InstallID, changed = id, true
	}
	if !changed {
		return st, nil
	}
	data, err := json.Marshal(st)
	if err != nil {
		return st, err
	}
	return st, WriteFileAtomic(b.path, data, 0o600)
}

// Get 当前设置（第一次调时生成 install_id）
func (b *SpeedBoard) Get() (SpeedBoardState, error) { return b.change(nil) }

// SetUpload 记下同意 / 不同意
func (b *SpeedBoard) SetUpload(upload bool) (SpeedBoardState, error) {
	return b.change(func(st *SpeedBoardState) bool {
		st.Upload = &upload
		return true
	})
}

// Reset 换一个 install_id（之后上传的结果算一台新设备，已上传的不动）
func (b *SpeedBoard) Reset() (SpeedBoardState, error) {
	return b.change(func(st *SpeedBoardState) bool {
		st.InstallID = ""
		return true
	})
}
