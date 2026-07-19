package doctor

import (
	"os"
)

func checkOutput(report *Report, path string) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		report.add(Check{Name: "output", Status: Fail, Message: err.Error()})
		return
	}
	file, err := os.CreateTemp(path, ".doctor-write-")
	if err != nil {
		report.add(Check{Name: "output", Status: Fail, Message: err.Error()})
		return
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.WriteString("ok"); err != nil {
		file.Close()
		report.add(Check{Name: "output", Status: Fail, Message: err.Error()})
		return
	}
	if err := file.Sync(); err != nil {
		file.Close()
		report.add(Check{Name: "output", Status: Fail, Message: err.Error()})
		return
	}
	if err := file.Close(); err != nil {
		report.add(Check{Name: "output", Status: Fail, Message: err.Error()})
		return
	}
	report.add(Check{Name: "output", Status: Pass, Message: path + " is writable"})
}
