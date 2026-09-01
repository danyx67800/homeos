package storage

// Exported for the external test package in health_test.go, which exercises the
// parsers against captured smartctl and lsblk output in testdata/.
var (
	ParseSMARTForTest = parseSMART
	ParseLsblkForTest = parseLsblk
)

// Also exported for the external tests: fstab rendering is the one function
// here that can make a machine unbootable, so it is tested directly.
var (
	RenderFstabForTest         = renderFstab
	ParseManagedEntriesForTest = parseManagedEntries
	FstabBeginForTest          = fstabBegin
	FstabEndForTest            = fstabEnd
)

type FstabEntryForTest = fstabEntry
