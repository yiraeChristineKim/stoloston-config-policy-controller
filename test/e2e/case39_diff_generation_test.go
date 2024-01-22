// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"open-cluster-management.io/config-policy-controller/test/utils"
)

var _ = Describe("Generate the diff", Ordered, func() {
	const (
		logPath          string = "../../build/_output/controller.log"
		configPolicyName string = "case39-policy-cfgmap-create"
		createYaml       string = "../resources/case39_diff_generation/case39-create-cfgmap-policy.yaml"
		updateYaml       string = "../resources/case39_diff_generation/case39-update-cfgmap-policy.yaml"
	)

	BeforeAll(func() {
		_, err := os.Stat(logPath)
		if err != nil {
			Skip(fmt.Sprintf("Skipping. Failed to find log file %s: %s", logPath, err.Error()))
		}
	})

	It("configmap should be created properly on the managed cluster", func() {
		By("Creating " + configPolicyName + " on managed")
		utils.Kubectl("apply", "-f", createYaml, "-n", testNamespace)
		plc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigPolicy,
			configPolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())
		Eventually(func() interface{} {
			managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigPolicy,
				configPolicyName, testNamespace, true, defaultTimeoutSeconds)

			return utils.GetStatusMessage(managedPlc)
		}, 120, 1).Should(Equal("configmaps [case39-map] found as specified in namespace default"))
	})

	It("configmap and status should be updated properly on the managed cluster", func() {
		By("Updating " + configPolicyName + " on managed")
		utils.Kubectl("apply", "-f", updateYaml, "-n", testNamespace)
		Eventually(func() interface{} {
			managedPlc := utils.GetWithTimeout(clientManagedDynamic, gvrConfigPolicy,
				configPolicyName, testNamespace, true, defaultTimeoutSeconds)

			return utils.GetStatusMessage(managedPlc)
		}, 30, 0.5).Should(Equal("configmaps [case39-map] was updated successfully in namespace default"))
	})

	It("diff should be logged by the controller", func() {
		By("Checking the controller logs")
		logFile, err := os.Open(logPath)
		Expect(err).ToNot(HaveOccurred())
		defer logFile.Close()

		diff := ""
		foundDiff := false
		logScanner := bufio.NewScanner(logFile)
		logScanner.Split(bufio.ScanLines)
		for logScanner.Scan() {
			line := logScanner.Text()
			if foundDiff && strings.HasPrefix(line, "\t{") {
				foundDiff = false
			} else if foundDiff || strings.Contains(line, "Logging the diff:") {
				foundDiff = true
			} else {
				continue
			}

			diff += line + "\n"
		}

		Expect(diff).Should(ContainSubstring(`Logging the diff:
--- default/case39-map : existing
+++ default/case39-map : updated
@@ -2,3 +2,3 @@
 data:
-  fieldToUpdate: "1"
+  fieldToUpdate: "2"
 kind: ConfigMap
	{"policy": "case39-policy-cfgmap-create", "name": "case39-map", "namespace": "default", "resource": "configmaps"}`))
	})

	AfterAll(func() {
		deleteConfigPolicies([]string{configPolicyName})
		utils.Kubectl("delete", "configmap", "case39-map", "--ignore-not-found")
	})
})
