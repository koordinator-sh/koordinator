package util

import "testing"

func Test_readIdleInfo(t *testing.T) {
	kidledReadIdleInfo("C:\\projects\\kkkkd\\koordinator\\memory.idle_page_stats")
}

func Test_GetIdleInfoFilePath(t *testing.T) {
	GetIdleInfoFilePath("/memory/")
}
