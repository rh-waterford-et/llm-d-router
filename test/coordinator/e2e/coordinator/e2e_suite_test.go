/*
Copyright 2026 The llm-d Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package coordinate2e runs end-to-end tests for the coordinator service
// against the e-p-d-pools topology: one InferencePool per phase (encode,
// prefill, decode), each with its own EPP, a hand-rolled standalone Envoy
// routing on EPP-Phase header, and the coordinator deployed as a pod.
// No Istio, no Gateway/HTTPRoute CRDs.
package coordinate2e

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	k8slog "sigs.k8s.io/controller-runtime/pkg/log"
	infextv1 "sigs.k8s.io/gateway-api-inference-extension/api/v1"

	infextv1a2 "github.com/llm-d/llm-d-router/apix/v1alpha2"
	"github.com/llm-d/llm-d-router/pkg/epp/util/env"
	testutils "github.com/llm-d/llm-d-router/test/utils"
)

const (
	kindClusterName = "e2e-coordinator-tests"

	defaultReadyTimeout    = 10 * time.Minute
	defaultInterval        = time.Second * 2
	defaultGatewayHostPort = 30080

	poolNameBase = "qwen3-vl-2b-instruct-inference-pool"
	eppName      = "e2e-epp"

	// 3-EPP topology (E2E_EPP_TOPOLOGY=3epp): one role-scoped EPP and
	// InferencePool per phase. Each EPP name doubles as its Service and
	// InferencePool endpointPickerRef name.
	eppNameEncode  = "e2e-epp-encode"
	eppNamePrefill = "e2e-epp-prefill"
	eppNameDecode  = "e2e-epp-decode"

	poolNameEncode  = "qwen3-vl-2b-instruct-encode-pool"
	poolNamePrefill = "qwen3-vl-2b-instruct-prefill-pool"
	poolNameDecode  = "qwen3-vl-2b-instruct-decode-pool"

	// EPP resources are the shared inference-gateway component's split files.
	// Gateway/HTTPRoute manifests in that component are unused: the coordinator
	// e2e fronts the EPP with a hand-rolled Envoy, matching the router e2e.
	eppManifest               = "../../../../deploy/components/inference-gateway/deployment.yaml"
	poolManifest              = "../../../../deploy/components/inference-gateway/inference-pools.yaml"
	eppRbacManifest           = "../../../../deploy/components/inference-gateway/rbac.yaml"
	eppServiceAccountManifest = "../../../../deploy/components/inference-gateway/service-accounts.yaml"
	eppServicesManifest       = "../../../../deploy/components/inference-gateway/services.yaml"

	epdPoolsKustomizeDir    = "../../../../deploy/environments/dev/coordinator-epd"
	coordinatorComponentDir = "../../../../deploy/coordinator"
	rendererManifest        = "../../../../deploy/environments/dev/e2e-infra/vllm-render.yaml"

	envoyManifest = "../../../../deploy/environments/dev/coordinator-e2e-infra/envoy.yaml"

	// sharedEnvoyManifest holds the Envoy Deployment and Service, identical
	// across topologies; the per-topology manifests carry only the routing
	// ConfigMap it mounts.
	sharedEnvoyManifest = "../../../../deploy/environments/dev/coordinator-e2e-infra/shared-envoy-resources.yaml"

	// 3-EPP topology manifests: the Envoy that fans EPP-Profile out to three
	// role-scoped ext_proc clusters, and the three role-scoped InferencePools.
	envoy3EPPManifest = "../../../../deploy/environments/dev/coordinator-e2e-infra/envoy-3-epp.yaml"
	pool3EPPManifest  = "../../../../deploy/environments/dev/coordinator-e2e-infra/inference-pools-3-epp.yaml"
	crdGIEPath        = "../../../../deploy/components/crds-gie"
)

var (
	baseGatewayPort = env.GetEnvInt("E2E_GATEWAY_PORT", defaultGatewayHostPort, ginkgo.GinkgoLogr)

	testConfig *testutils.TestConfig

	keepClusterOnFailure = env.GetEnvBool("E2E_KEEP_CLUSTER_ON_FAILURE", false, ginkgo.GinkgoLogr)
	printLogs            = env.GetEnvBool("E2E_PRINT_LOGS", false, ginkgo.GinkgoLogr)

	// threeEPP selects the 3-EPP topology (one role-scoped EPP + InferencePool per
	// phase) instead of the default single-EPP topology. See envoy3EPPManifest and
	// the eppConfigLeastBusy (encode, decode) and eppConfigPrefill configs.
	threeEPP = env.GetEnvString("E2E_EPP_TOPOLOGY", "single", ginkgo.GinkgoLogr) == "3epp"

	containerRuntime = env.GetEnvString("CONTAINER_RUNTIME", "docker", ginkgo.GinkgoLogr)
	eppImage         = env.GetEnvString("EPP_IMAGE", "ghcr.io/llm-d/llm-d-router-endpoint-picker:dev", ginkgo.GinkgoLogr)
	vllmSimImage     = env.GetEnvString("VLLM_IMAGE", "ghcr.io/llm-d/llm-d-inference-sim:v0.10.2", ginkgo.GinkgoLogr)
	vllmRenderImage  = env.GetEnvString("VLLM_RENDER_IMAGE", "vllm/vllm-openai-cpu:v0.21.0", ginkgo.GinkgoLogr)
	vllmRenderPort   = env.GetEnvString("VLLM_RENDER_PORT", "8082", ginkgo.GinkgoLogr)
	coordinatorImage = env.GetEnvString("COORDINATOR_IMAGE", "", ginkgo.GinkgoLogr)
	modelName        = env.GetEnvString("MODEL_NAME", "Qwen/Qwen3-VL-2B-Instruct", ginkgo.GinkgoLogr)

	numProcesses = env.GetEnvInt("E2E_NUM_PROCS", 1, ginkgo.GinkgoLogr)

	// baseNsName is the base of the namespace in which the K8S objects will be created.
	baseNsName = env.GetEnvString("NAMESPACE", testutils.DefaultNsName(numProcesses, "e2e-coordinator"), ginkgo.GinkgoLogr)
	k8sContext = env.GetEnvString("K8S_CONTEXT", "", ginkgo.GinkgoLogr)

	readyTimeout = env.GetEnvDuration("READY_TIMEOUT", defaultReadyTimeout, ginkgo.GinkgoLogr)

	portForwardSessions []*gexec.Session
	rendererObjects     []string
	stableInfraObjects  []string
	createdNameSpace    bool
)

// roleEPP describes one EPP to create: its EPP/Service name, the InferencePool
// it backs, and its scheduling config. The single-EPP topology has one entry
// (all three roles, eppConfig); the 3-EPP topology has one per role.
type roleEPP struct {
	role     string
	eppName  string
	poolName string
	config   string
}

// eppsToCreate returns the EPPs for the active topology, in encode/prefill/decode
// order for 3-EPP.
func eppsToCreate() []roleEPP {
	if threeEPP {
		return []roleEPP{
			{role: "encode", eppName: eppNameEncode, poolName: poolNameEncode, config: eppConfigLeastBusy},
			{role: "prefill", eppName: eppNamePrefill, poolName: poolNamePrefill, config: eppConfigPrefill},
			{role: "decode", eppName: eppNameDecode, poolName: poolNameDecode, config: eppConfigLeastBusy},
		}
	}
	return []roleEPP{{eppName: eppName, poolName: poolNameBase, config: eppConfig}}
}

// poolNames returns the InferencePool names for the active topology, derived
// from eppsToCreate so the topology branch lives in one place.
func poolNames() []string {
	epps := eppsToCreate()
	names := make([]string, len(epps))
	for i, e := range epps {
		names[i] = e.poolName
	}
	return names
}

func TestCoordinatorE2E(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Coordinator E2E Suite")
}

var _ = ginkgo.BeforeSuite(func() {
	gomega.Expect(coordinatorImage).NotTo(gomega.BeEmpty(), "COORDINATOR_IMAGE must be set")

	testutils.RequireParallelProcessesMatch(numProcesses)

	if k8sContext == "" {
		setupK8sCluster()
	}
	testConfig = testutils.NewTestConfig(k8sContext)
	setupK8sClient()
	setupNameSpace()

	// Base infra (CRDs, RBAC, Envoy) is created here on suite-owned kind clusters.
	// With K8S_CONTEXT set, base infra is assumed pre-deployed; the per-test
	// workload (EPPs, pools, vLLM workers, coordinator) is created in the test body.
	if k8sContext == "" {
		setupInfra()
	} else {
		// Base infra (including Envoy) is pre-deployed; forward the gateway so
		// the test can post to it. The kind nodePort mapping is unavailable here.
		startPortForward("service/envoy", strconv.Itoa(getGatewayPort()), "8081")
	}

	rendererObjects = createRenderer()

	// Coordinator and EPP Services/RBAC are created once and kept stable across
	// specs (see createStableInfra).
	createStableInfra()
})

var _ = ginkgo.ReportAfterSuite("cleanup", func(report ginkgo.Report) {
	if !report.SuiteSucceeded {
		for idx := range numProcesses {
			testutils.DumpPodsAndLogs(testConfig, testutils.NamespaceForProcess(baseNsName, numProcesses, idx+1))
		}
	}

	if k8sContext == "" && keepClusterOnFailure && !report.SuiteSucceeded {
		ginkgo.By("Keeping kind cluster " + kindClusterName + " due to suite failure (E2E_KEEP_CLUSTER_ON_FAILURE=true)")
		return
	}
	nsName := getNamespace()
	if len(rendererObjects) > 0 {
		testutils.DeleteObjects(testConfig, rendererObjects, nsName)
	}
	if len(stableInfraObjects) > 0 {
		testutils.DeleteObjects(testConfig, stableInfraObjects, nsName)
	}
	for _, session := range portForwardSessions {
		session.Terminate()
	}
	if createdNameSpace {
		err := testConfig.KubeCli.CoreV1().Namespaces().Delete(testConfig.Context, nsName, metav1.DeleteOptions{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}
	if k8sContext != "" {
		return
	}
	ginkgo.By("Deleting kind cluster " + kindClusterName)
	command := exec.Command("kind", "delete", "cluster", "--name", kindClusterName)
	session, err := gexec.Start(command, ginkgo.GinkgoWriter, ginkgo.GinkgoWriter)
	if err != nil {
		ginkgo.GinkgoLogr.Error(err, "Failed to delete kind cluster")
		return
	}
	gomega.Eventually(session).WithTimeout(60 * time.Second).Should(gexec.Exit())
})

// startPortForward forwards a local port to the given target (e.g.
// "deployment/llm-d-coordinator" or "service/envoy"). Used when running against
// an existing cluster (K8S_CONTEXT set), where the kind nodePort mapping is not
// available. Sessions are tracked for teardown in AfterSuite.
func startPortForward(target, localPort, remotePort string) {
	command := exec.Command("kubectl", "port-forward", target,
		localPort+":"+remotePort,
		"--context="+k8sContext, "--namespace="+getNamespace())
	session, err := gexec.Start(command, ginkgo.GinkgoWriter, ginkgo.GinkgoWriter)
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
	portForwardSessions = append(portForwardSessions, session)
}

func setupK8sCluster() {
	// extraPortMappings is substituted into `extraPortMappings: ${EXTRA_PORT_MAPPINGS}` in the Kind
	// cluster configuration below; keep its indentation in sync with testutils.BuildExtraPortMappings.
	extraPortMappings := testutils.BuildExtraPortMappings(numProcesses,
		[2]int{defaultGatewayHostPort, baseGatewayPort},
	)

	command := exec.Command("kind", "create", "cluster", "--name", kindClusterName, "--config", "-")
	stdin, err := command.StdinPipe()
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
	go func() {
		defer func() {
			err := stdin.Close()
			gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
		}()
		clusterConfig := strings.ReplaceAll(kindClusterConfig, "${EXTRA_PORT_MAPPINGS}", extraPortMappings)
		_, err := io.WriteString(stdin, clusterConfig)
		gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
	}()
	session, err := gexec.Start(command, ginkgo.GinkgoWriter, ginkgo.GinkgoWriter)
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
	gomega.Eventually(session).WithTimeout(600 * time.Second).Should(gexec.Exit(0))

	cleanNodeResolvConf()
	fixCoreDNS()

	images := []string{vllmSimImage, eppImage, coordinatorImage}
	if vllmRenderImage != vllmSimImage {
		images = append(images, vllmRenderImage)
	}
	for _, img := range images {
		kindLoadImage(img)
	}
}

// cleanNodeResolvConf replaces the Kind node's /etc/resolv.conf with clean
// public DNS entries, removing any host search domains. The Kind node
// inherits the host's resolv.conf, and kubelet appends its search domains
// to every pod's resolv.conf. With ndots:5, Go's resolver tries appending
// search domains before the bare FQDN for names with fewer than 5 dots.
// ISP or local wildcard domains can resolve those suffixed names to
// 127.0.0.1, which the SSRF guard in replace-media-urls blocks.
func cleanNodeResolvConf() {
	nodeName := kindClusterName + "-control-plane"
	out, err := exec.Command(containerRuntime, "exec", nodeName,
		"cat", "/etc/resolv.conf").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "search") {
			ginkgo.By("Cleaning Kind node resolv.conf to remove host search domains")
			cmd := exec.Command(containerRuntime, "exec", nodeName,
				"/bin/bash", "-c", `printf 'nameserver 8.8.8.8\nnameserver 8.8.4.4\n' > /etc/resolv.conf`)
			session, err := gexec.Start(cmd, ginkgo.GinkgoWriter, ginkgo.GinkgoWriter)
			gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
			gomega.Eventually(session).WithTimeout(30 * time.Second).Should(gexec.Exit(0))
			return
		}
	}
}

// fixCoreDNS patches the CoreDNS Corefile to forward external queries to
// the host's upstream DNS servers instead of /etc/resolv.conf. Inside a Kind
// node container, /etc/resolv.conf typically points to a localhost stub
// resolver (systemd-resolved at 127.0.0.53, Docker DNS at 127.0.0.11, or
// dnsmasq at 127.0.0.1). CoreDNS would forward to that loopback address
// inside its own pod network namespace, creating a forwarding loop that
// crashes CoreDNS or silently breaks DNS resolution for all pods.
func fixCoreDNS() {
	upstream := upstreamDNS()
	ginkgo.By("Patching CoreDNS to forward to: " + upstream)

	corefile := fmt.Sprintf(`.:53 {
    errors
    health {
       lameduck 5s
    }
    ready
    kubernetes cluster.local in-addr.arpa ip6.arpa {
       pods insecure
       fallthrough in-addr.arpa ip6.arpa
       ttl 30
    }
    prometheus :9153
    forward . %s
    cache 30
    loop
    reload
    loadbalance
}
`, upstream)

	patch, err := json.Marshal(map[string]map[string]string{
		"data": {"Corefile": corefile},
	})
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())

	cmd := exec.Command("kubectl", "patch", "configmap", "coredns",
		"-n", "kube-system", "--type", "merge", "-p", string(patch))
	session, err := gexec.Start(cmd, ginkgo.GinkgoWriter, ginkgo.GinkgoWriter)
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
	gomega.Eventually(session).WithTimeout(30 * time.Second).Should(gexec.Exit(0))

	// Delete existing CoreDNS pods so the deployment controller recreates
	// them with the updated configmap immediately. A rollout restart is
	// slower and can briefly serve the old Corefile during the rolling update.
	ginkgo.By("Deleting CoreDNS pods to apply new Corefile")
	cmd = exec.Command("kubectl", "delete", "pods", "-n", "kube-system", "-l", "k8s-app=kube-dns")
	session, err = gexec.Start(cmd, ginkgo.GinkgoWriter, ginkgo.GinkgoWriter)
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
	gomega.Eventually(session).WithTimeout(30 * time.Second).Should(gexec.Exit(0))

	ginkgo.By("Waiting for CoreDNS to become ready")
	cmd = exec.Command("kubectl", "rollout", "status", "deployment/coredns",
		"-n", "kube-system", "--timeout=120s")
	session, err = gexec.Start(cmd, ginkgo.GinkgoWriter, ginkgo.GinkgoWriter)
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
	gomega.Eventually(session).WithTimeout(150 * time.Second).Should(gexec.Exit(0))

	// Verify the configmap was updated and CoreDNS pods are not crash-looping.
	ginkgo.By("Verifying CoreDNS Corefile contains patched upstream")
	gomega.Eventually(func() string {
		cmd := exec.Command("kubectl", "get", "configmap", "coredns",
			"-n", "kube-system", "-o", "jsonpath={.data.Corefile}")
		out, err := cmd.Output()
		if err != nil {
			return fmt.Sprintf("kubectl error: %v", err)
		}
		return string(out)
	}, 30*time.Second, 2*time.Second).Should(
		gomega.And(
			gomega.ContainSubstring("8.8.8.8"),
			gomega.Not(gomega.ContainSubstring("/etc/resolv.conf")),
		),
		"CoreDNS Corefile must forward to real upstream DNS, not /etc/resolv.conf",
	)

	ginkgo.By("Verifying CoreDNS pods are running")
	gomega.Eventually(func() bool {
		cmd := exec.Command("kubectl", "get", "pods", "-n", "kube-system",
			"-l", "k8s-app=kube-dns", "-o", "jsonpath={.items[*].status.phase}")
		out, err := cmd.Output()
		if err != nil {
			return false
		}
		phases := strings.Fields(string(out))
		if len(phases) == 0 {
			return false
		}
		for _, p := range phases {
			if p != "Running" {
				return false
			}
		}
		return true
	}, 60*time.Second, 5*time.Second).Should(gomega.BeTrue(), "all CoreDNS pods should be Running")

	// Dump CoreDNS logs so loop-detection crashes or upstream connectivity
	// failures are visible in the test output.
	ginkgo.By("CoreDNS pod logs after fix")
	cmd = exec.Command("kubectl", "logs", "-n", "kube-system", "-l", "k8s-app=kube-dns", "--tail=30")
	session, err = gexec.Start(cmd, ginkgo.GinkgoWriter, ginkgo.GinkgoWriter)
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
	gomega.Eventually(session).WithTimeout(30 * time.Second).Should(gexec.Exit(0))
}

// upstreamDNS discovers the host's real upstream DNS servers by reading
// systemd-resolved's configuration (Fedora, Ubuntu) or /etc/resolv.conf.
// Localhost stubs (127.x.x.x, ::1) are filtered out because they would
// create a forwarding loop inside CoreDNS's pod. Google public DNS is
// appended as a fallback. If no upstream is discovered, returns Google DNS.
func upstreamDNS() string {
	for _, path := range []string{
		"/run/systemd/resolve/resolv.conf",
		"/etc/resolv.conf",
	} {
		servers := parseNameserversFromFile(path)
		if len(servers) > 0 {
			result := strings.Join(servers, " ")
			if !strings.Contains(result, "8.8.8.8") {
				result += " 8.8.8.8"
			}
			return result
		}
	}
	return "8.8.8.8 8.8.4.4"
}

func parseNameserversFromFile(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var servers []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "nameserver") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		addr := fields[1]
		if strings.HasPrefix(addr, "127.") || addr == "::1" {
			continue
		}
		servers = append(servers, addr)
	}
	return servers
}

func kindLoadImage(image string) {
	ginkgo.By(fmt.Sprintf("Loading %s into the cluster %s using %s", image, kindClusterName, containerRuntime))
	if containerRuntime == "docker" {
		nodeName := kindClusterName + "-control-plane"
		save := exec.Command("docker", "save", image)
		importCmd := exec.Command("docker", "exec", "--privileged", "-i", nodeName,
			"ctr", "--namespace=k8s.io", "images", "import", "--digests", "--snapshotter=overlayfs", "-")
		pipe, err := save.StdoutPipe()
		gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
		importCmd.Stdin = pipe
		importCmd.Stdout = ginkgo.GinkgoWriter
		importCmd.Stderr = ginkgo.GinkgoWriter
		gomega.Expect(save.Start()).ShouldNot(gomega.HaveOccurred())
		gomega.Expect(importCmd.Start()).ShouldNot(gomega.HaveOccurred())
		gomega.Expect(save.Wait()).ShouldNot(gomega.HaveOccurred())
		gomega.Expect(importCmd.Wait()).ShouldNot(gomega.HaveOccurred())
		return
	}
	command := exec.Command("kind", "--name", kindClusterName, "load", "docker-image", image)
	session, err := gexec.Start(command, ginkgo.GinkgoWriter, ginkgo.GinkgoWriter)
	gomega.Expect(err).ShouldNot(gomega.HaveOccurred())
	gomega.Eventually(session).WithTimeout(600 * time.Second).Should(gexec.Exit(0))
}

func setupK8sClient() {
	k8sCfg, err := config.GetConfigWithContext(k8sContext)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.ExpectWithOffset(1, k8sCfg).NotTo(gomega.BeNil())

	gomega.Expect(clientgoscheme.AddToScheme(testConfig.Scheme)).To(gomega.Succeed())
	gomega.Expect(infextv1.Install(testConfig.Scheme)).To(gomega.Succeed())
	gomega.Expect(apiextv1.AddToScheme(testConfig.Scheme)).To(gomega.Succeed())
	gomega.Expect(infextv1a2.Install(testConfig.Scheme)).To(gomega.Succeed())

	testConfig.CreateCli()
	k8slog.SetLogger(ginkgo.GinkgoLogr)
}

// getGatewayPort returns the envoy gateway's NodePort for this process. See testutils.ProcessPort.
func getGatewayPort() int {
	return testutils.ProcessPort(baseGatewayPort)
}

// getNamespace returns the namespace being used by the current process. Each
// parallel process is assigned its own namespace to provide isolation between
// the tests running in it. See testutils.Namespace.
func getNamespace() string {
	return testutils.Namespace(baseNsName, numProcesses)
}

func gatewayBaseURL() string {
	return testutils.LocalhostURL(getGatewayPort())
}

const kindClusterConfig = `
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- image: kindest/node:v1.31.12
  extraPortMappings:${EXTRA_PORT_MAPPINGS}
`
