package leveldb

import (
	"testing"

	"github.com/5kbpers/goleveldb/leveldb/testutil"
)

func TestLevelDB(t *testing.T) {
	testutil.RunSuite(t, "LevelDB Suite")
}
