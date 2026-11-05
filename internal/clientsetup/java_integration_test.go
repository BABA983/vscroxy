package clientsetup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
)

// TestJavaTruststoreImportsCACertsAndTheAppCA is opt-in because it needs a JDK.
// Run it with:
//
//	VSCROXY_JAVA_TEST=1 go test ./internal/clientsetup -run TestJavaTruststoreImportsCACertsAndTheAppCA -count=1 -v
func TestJavaTruststoreImportsCACertsAndTheAppCA(t *testing.T) {
	if os.Getenv("VSCROXY_JAVA_TEST") == "" {
		t.Skip("set VSCROXY_JAVA_TEST=1 with a JDK on PATH")
	}
	if _, err := exec.LookPath("keytool"); err != nil {
		t.Skip("keytool is not installed")
	}
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, testCertificate(t, "vscroxy test CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	keystore := filepath.Join(dir, "java", "truststore.p12")
	commands, err := ImportJavaCA(Proxy{Addr: "127.0.0.1:7890", CAPath: caPath}, keystore)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 3 {
		t.Fatalf("commands: %+v", commands)
	}
	out, err := exec.Command("keytool", "-list", "-keystore", keystore, "-storepass", "changeit").CombinedOutput()
	if err != nil {
		t.Fatalf("list truststore: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "vscroxy-ca") {
		t.Fatalf("truststore missing vscroxy-ca: %s", out)
	}
	if strings.Count(string(out), "Certificate fingerprint") < 2 {
		t.Fatalf("truststore did not keep the JDK roots: %s", out)
	}
}

// TestJavaRequestUsesTheProxyAndTruststore is opt-in because it needs a JDK and
// internet access. It runs a small HttpsURLConnection program with the JVM
// options the app shows, decrypts example.com and tunnels www.iana.org, and
// checks that both requests reached the proxy.
func TestJavaRequestUsesTheProxyAndTruststore(t *testing.T) {
	if os.Getenv("VSCROXY_JAVA_TEST") == "" {
		t.Skip("set VSCROXY_JAVA_TEST=1 with a JDK on PATH")
	}
	for _, tool := range []string{"java", "javac"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	dir := t.TempDir()
	srv, err := capture.New("127.0.0.1:0", dir)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetSSL(capture.SSLConfig{Hosts: []string{"example.com"}})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	caPath := filepath.Join(dir, "mitmproxy-ca-cert.pem")
	proxy := Proxy{Addr: srv.Addr(), CAPath: caPath}
	keystore := filepath.Join(dir, "java", "truststore.p12")
	if _, err := ImportJavaCA(proxy, keystore); err != nil {
		t.Fatal(err)
	}
	jvm, err := JVMArgs(proxy, keystore)
	if err != nil {
		t.Fatal(err)
	}

	source := `import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;

public class Main {
    public static void main(String[] args) throws Exception {
        for (String raw : args) {
            HttpURLConnection c = (HttpURLConnection) new URL(raw).openConnection();
            c.setConnectTimeout(20000);
            c.setReadTimeout(20000);
            int code = c.getResponseCode();
            System.out.println(raw + " " + code);
            InputStream in = code >= 400 ? c.getErrorStream() : c.getInputStream();
            if (in != null) {
                in.readAllBytes();
                in.close();
            }
        }
    }
}
`
	sourcePath := filepath.Join(dir, "Main.java")
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	classes := filepath.Join(dir, "classes")
	if err := os.MkdirAll(classes, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("javac", "-d", classes, sourcePath).CombinedOutput(); err != nil {
		t.Fatalf("javac: %v: %s", err, out)
	}
	args := append(jvm, "-cp", classes, "Main", "https://example.com/", "https://www.iana.org/")
	out, err := exec.Command("java", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("java: %v: %s", err, out)
	}
	for _, want := range []string{"https://example.com/ 200", "https://www.iana.org/ 200"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("java output missing %q: %s", want, out)
		}
	}
	seen := map[string]bool{}
	deadline := time.After(10 * time.Second)
	for len(seen) < 2 {
		select {
		case flow := <-srv.Flows():
			seen[flow.Host] = true
		case <-deadline:
			t.Fatalf("requests did not reach the proxy: %v", seen)
		}
	}
}
