package doctor

import (
	"context"
	"strings"
	"testing"

	"forge_worker/internal/runner"
)

func TestCheckVersionIncludesCommandWhenDebugEnabled(t *testing.T) {
	report := Report{OK: true}
	checkPackager(context.Background(), fakeDoctorRunner{stdout: "packager version 2.6.1"}, &report, "/usr/bin/packager", Options{Debug: true})
	if !report.OK {
		t.Fatalf("expected packager version check to pass")
	}
	if len(report.Checks) != 1 {
		t.Fatalf("checks = %d", len(report.Checks))
	}
	check := report.Checks[0]
	if check.Name != "packager.version" || check.Message != "packager version 2.6.1" || len(check.Command) == 0 {
		t.Fatalf("expected command on check: %+v", check)
	}
	if got := strings.Join(check.Command, " "); got != "/usr/bin/packager --version" {
		t.Fatalf("command = %s", got)
	}
	if human := report.Human(); !strings.Contains(human, "command: /usr/bin/packager --version") || !strings.Contains(human, "packager version 2.6.1") {
		t.Fatalf("human report did not include command: %s", human)
	}
}

func TestCheckVersionOmitsCommandByDefault(t *testing.T) {
	report := Report{OK: true}
	checkPackager(context.Background(), fakeDoctorRunner{stdout: "packager version 2.6.1"}, &report, "/usr/bin/packager", Options{})
	if !report.OK {
		t.Fatalf("expected packager version check to pass")
	}
	if len(report.Checks) != 1 || len(report.Checks[0].Command) != 0 {
		t.Fatalf("expected no command by default: %+v", report.Checks)
	}
}

func TestReportHumanGroupsChecksBySection(t *testing.T) {
	report := Report{
		OK:        false,
		CheckedAt: "2026-07-15T00:00:00Z",
		Checks: []Check{
			{Name: "config", Status: Pass, Message: "configuration is valid"},
			{Name: "ffmpeg.version", Status: Pass, Message: "ffmpeg version 7.1", Command: []string{"/usr/bin/ffmpeg", "-version"}},
			{Name: "vips.avif", Status: Fail, Message: "HEIF/AVIF support was not listed by vips -l", Command: []string{"/usr/bin/vips", "-l"}},
			{Name: "output", Status: Pass, Message: "/work/output is writable"},
		},
	}

	human := report.Human()
	for _, expected := range []string{
		"forge-worker doctor",
		"checked_at: 2026-07-15T00:00:00Z",
		"summary: 3 passed, 0 warning, 1 failed",
		"Configuration\n  PASS config",
		"Tools\n  PASS ffmpeg.version",
		"FAIL vips.avif",
		"command: /usr/bin/vips -l",
		"Storage\n  PASS output",
		"doctor: one or more required checks failed",
	} {
		if !strings.Contains(human, expected) {
			t.Fatalf("human report missing %q:\n%s", expected, human)
		}
	}
}

func TestCheckVIPSUsesFeatureListForAVIFSupport(t *testing.T) {
	report := Report{OK: true}
	runner := fakeDoctorRunner{run: func(spec runner.Spec) (runner.Result, error) {
		switch {
		case spec.Name == "/usr/bin/vips" && strings.Join(spec.Args, " ") == "--version":
			return runner.Result{Stdout: "vips-8.15.3"}, nil
		case spec.Name == "/usr/bin/vips" && strings.Join(spec.Args, " ") == "-l":
			return runner.Result{Stdout: "VipsForeignSaveHeifFile\n"}, nil
		case spec.Name == "heif-enc" && strings.Join(spec.Args, " ") == "--list-encoders":
			return runner.Result{Stdout: "AVC encoders:\nAVIF encoders:\n- aom = AOMedia Project AV1 Encoder v3.12.1 [default]\nHEIC encoders:\n"}, nil
		default:
			t.Fatalf("unexpected command: %s %s", spec.Name, strings.Join(spec.Args, " "))
			return runner.Result{}, nil
		}
	}}

	checkVIPS(context.Background(), runner, &report, "/usr/bin/vips", Options{Debug: true})
	if !report.OK {
		t.Fatalf("expected vips checks to pass: %+v", report.Checks)
	}
	check := findCheck(t, report, "vips.avif")
	if check.Status != Pass || !strings.Contains(check.Message, "HEIF/AVIF support listed") {
		t.Fatalf("expected vips.avif pass with heif message: %+v", check)
	}
	if got := strings.Join(check.Command, " "); got != "/usr/bin/vips -l" {
		t.Fatalf("command = %s", got)
	}
	encode := findCheck(t, report, "heif-enc.avif")
	if encode.Status != Pass || !strings.Contains(encode.Message, "aom") || strings.Join(encode.Command, " ") != "heif-enc --list-encoders" {
		t.Fatalf("expected heif-enc AVIF encoder pass with command: %+v", encode)
	}
}

func TestCheckVIPSFailsWhenHEIFEncHasNoAVIFEncoder(t *testing.T) {
	report := Report{OK: true}
	runner := fakeDoctorRunner{run: func(spec runner.Spec) (runner.Result, error) {
		switch {
		case spec.Name == "/usr/bin/vips" && strings.Join(spec.Args, " ") == "--version":
			return runner.Result{Stdout: "vips-8.15.3"}, nil
		case spec.Name == "/usr/bin/vips" && strings.Join(spec.Args, " ") == "-l":
			return runner.Result{Stdout: "VipsForeignSaveHeifFile\n"}, nil
		case spec.Name == "heif-enc" && strings.Join(spec.Args, " ") == "--list-encoders":
			return runner.Result{Stdout: "AVC encoders:\nAVIF encoders:\nHEIC encoders:\n- x265 = x265 HEVC encoder\n"}, nil
		default:
			t.Fatalf("unexpected command: %s %s", spec.Name, strings.Join(spec.Args, " "))
			return runner.Result{}, nil
		}
	}}

	checkVIPS(context.Background(), runner, &report, "/usr/bin/vips", Options{Debug: true})
	if report.OK {
		t.Fatalf("expected heif-enc AVIF check to fail: %+v", report.Checks)
	}
	check := findCheck(t, report, "heif-enc.avif")
	if check.Status != Fail || !strings.Contains(check.Message, "heif-enc --list-encoders") {
		t.Fatalf("unexpected heif-enc AVIF failure: %+v", check)
	}
}

func TestCheckVIPSFailsWhenHEIFIsNotListed(t *testing.T) {
	report := Report{OK: true}
	runner := fakeDoctorRunner{run: func(spec runner.Spec) (runner.Result, error) {
		switch strings.Join(spec.Args, " ") {
		case "--version":
			return runner.Result{Stdout: "vips-8.15.3"}, nil
		case "-l":
			return runner.Result{Stdout: "VipsForeignSavePngFile\n"}, nil
		default:
			t.Fatalf("unexpected command: %s %s", spec.Name, strings.Join(spec.Args, " "))
			return runner.Result{}, nil
		}
	}}

	checkVIPS(context.Background(), runner, &report, "/usr/bin/vips", Options{Debug: true})
	if report.OK {
		t.Fatalf("expected vips.avif to fail: %+v", report.Checks)
	}
	check := findCheck(t, report, "vips.avif")
	if check.Status != Fail || !strings.Contains(check.Message, "vips -l") {
		t.Fatalf("expected vips.avif failure mentioning vips -l: %+v", check)
	}
	if got := strings.Join(check.Command, " "); got != "/usr/bin/vips -l" {
		t.Fatalf("command = %s", got)
	}
}

type fakeDoctorRunner struct {
	stdout string
	stderr string
	err    error
	run    func(runner.Spec) (runner.Result, error)
}

func (r fakeDoctorRunner) Run(_ context.Context, spec runner.Spec) (runner.Result, error) {
	if r.run != nil {
		return r.run(spec)
	}
	return runner.Result{Stdout: r.stdout, Stderr: r.stderr, ExitCode: 0}, r.err
}

func findCheck(t *testing.T, report Report, name string) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("missing check %s in %+v", name, report.Checks)
	return Check{}
}
