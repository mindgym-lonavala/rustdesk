package mail

import (
	"strings"
	"testing"
	"time"
)

func TestValidAddress(t *testing.T) {
	if a, err := ValidAddress("Asha <asha@example.org>"); err != nil || a != "asha@example.org" {
		t.Fatalf("%q %v", a, err)
	}
	for _, bad := range []string{"", "nope", "a@b.org, c@d.org", "a@b.org\r\nBcc: x@y.org"} {
		if _, err := ValidAddress(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestBuildRejectsHeaderInjectionAndEncodes(t *testing.T) {
	if _, err := Build("it@example.org", "a@b.org", "hi\r\nBcc: x@y.org", "x", time.Now()); err == nil {
		t.Fatal("header injection accepted")
	}
	msg, err := Build("it@example.org", "a@b.org", "मदत — help", "Line 1\nLine 2 = ok", time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	s := string(msg)
	for _, want := range []string{"To: <a@b.org>\r\n", "Subject: =?utf-8?q?", "Line 1\r\nLine 2 =3D ok"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
}
