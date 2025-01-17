package e2e

import (
	"encoding/json"
	"fmt"
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	policyv1 "open-cluster-management.io/config-policy-controller/api/v1"
	policyv1beta1 "open-cluster-management.io/config-policy-controller/api/v1beta1"
	"open-cluster-management.io/config-policy-controller/pkg/common"
	"open-cluster-management.io/config-policy-controller/test/utils"
)

var _ = Describe("Test installing an operator from OperatorPolicy", Ordered, func() {
	const (
		opPolTestNS          = "operator-policy-testns"
		parentPolicyYAML     = "../resources/case38_operator_install/parent-policy.yaml"
		parentPolicyName     = "parent-policy"
		eventuallyTimeout    = 10
		consistentlyDuration = 5
		olmWaitTimeout       = 45
	)

	check := func(
		polName string,
		wantNonCompliant bool,
		expectedRelatedObjs []policyv1.RelatedObject,
		expectedCondition metav1.Condition,
		expectedEventMsgSnippet string,
	) {
		var debugMessage string

		defer func() {
			if CurrentSpecReport().Failed() {
				GinkgoWriter.Println(debugMessage)
			}
		}()

		checkFunc := func(g Gomega) {
			GinkgoHelper()

			unstructPolicy := utils.GetWithTimeout(clientManagedDynamic, gvrOperatorPolicy, polName,
				opPolTestNS, true, eventuallyTimeout)

			unstructured.RemoveNestedField(unstructPolicy.Object, "metadata", "managedFields")

			policyJSON, err := json.MarshalIndent(unstructPolicy.Object, "", "  ")
			g.Expect(err).NotTo(HaveOccurred())

			debugMessage = fmt.Sprintf("Debug info for failure.\npolicy JSON: %s\nwanted related objects: %+v\n"+
				"wanted condition: %+v\n", string(policyJSON), expectedRelatedObjs, expectedCondition)

			policy := policyv1beta1.OperatorPolicy{}
			err = json.Unmarshal(policyJSON, &policy)
			g.Expect(err).NotTo(HaveOccurred())

			if wantNonCompliant {
				g.Expect(policy.Status.ComplianceState).To(Equal(policyv1.NonCompliant))
			}

			if len(expectedRelatedObjs) != 0 {
				matchingRelated := policy.Status.RelatedObjsOfKind(expectedRelatedObjs[0].Object.Kind)
				g.Expect(matchingRelated).To(HaveLen(len(expectedRelatedObjs)))

				for _, expectedRelObj := range expectedRelatedObjs {
					foundMatchingName := false
					unnamed := expectedRelObj.Object.Metadata.Name == ""

					for _, actualRelObj := range matchingRelated {
						if unnamed || actualRelObj.Object.Metadata.Name == expectedRelObj.Object.Metadata.Name {
							foundMatchingName = true
							g.Expect(actualRelObj.Compliant).To(Equal(expectedRelObj.Compliant))
							g.Expect(actualRelObj.Reason).To(Equal(expectedRelObj.Reason))
						}
					}

					g.Expect(foundMatchingName).To(BeTrue())
				}
			}

			idx, actualCondition := policy.Status.GetCondition(expectedCondition.Type)
			g.Expect(idx).NotTo(Equal(-1))
			g.Expect(actualCondition.Status).To(Equal(expectedCondition.Status))
			g.Expect(actualCondition.Reason).To(Equal(expectedCondition.Reason))
			g.Expect(actualCondition.Message).To(MatchRegexp(
				fmt.Sprintf(".*%v.*", regexp.QuoteMeta(expectedCondition.Message))))

			events := utils.GetMatchingEvents(
				clientManaged, opPolTestNS, parentPolicyName, "", expectedEventMsgSnippet, eventuallyTimeout,
			)
			g.Expect(events).NotTo(BeEmpty())

			for _, event := range events {
				g.Expect(event.Annotations[common.ParentDBIDAnnotation]).To(
					Equal("124"), common.ParentDBIDAnnotation+" should have the correct value",
				)
				g.Expect(event.Annotations[common.PolicyDBIDAnnotation]).To(
					Equal("64"), common.PolicyDBIDAnnotation+" should have the correct value",
				)
			}
		}

		EventuallyWithOffset(1, checkFunc, eventuallyTimeout, 3).Should(Succeed())
		ConsistentlyWithOffset(1, checkFunc, consistentlyDuration, 1).Should(Succeed())
	}

	Describe("Testing OperatorGroup behavior when it is not specified in the policy", Ordered, func() {
		const (
			opPolYAML        = "../resources/case38_operator_install/operator-policy-no-group.yaml"
			opPolName        = "oppol-no-group"
			extraOpGroupYAML = "../resources/case38_operator_install/extra-operator-group.yaml"
			extraOpGroupName = "extra-operator-group"
		)
		BeforeAll(func() {
			utils.Kubectl("create", "ns", opPolTestNS)
			DeferCleanup(func() {
				utils.Kubectl("delete", "ns", opPolTestNS)
			})

			createObjWithParent(parentPolicyYAML, parentPolicyName,
				opPolYAML, opPolTestNS, gvrPolicy, gvrOperatorPolicy)
		})

		It("Should initially be NonCompliant", func() {
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "OperatorGroup",
						APIVersion: "operators.coreos.com/v1",
						Metadata: policyv1.ObjectMetadata{
							Name:      "-",
							Namespace: opPolTestNS,
						},
					},
					Compliant: "NonCompliant",
					Reason:    "Resource not found but should exist",
				}},
				metav1.Condition{
					Type:    "OperatorGroupCompliant",
					Status:  metav1.ConditionFalse,
					Reason:  "OperatorGroupMissing",
					Message: "the OperatorGroup required by the policy was not found",
				},
				"the OperatorGroup required by the policy was not found",
			)
		})
		It("Should create the OperatorGroup when it is enforced", func() {
			utils.Kubectl("patch", "operatorpolicy", opPolName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/remediationAction", "value": "enforce"}]`)
			check(
				opPolName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "OperatorGroup",
						APIVersion: "operators.coreos.com/v1",
					},
					Compliant: "Compliant",
					Reason:    "Resource found as expected",
				}},
				metav1.Condition{
					Type:    "OperatorGroupCompliant",
					Status:  metav1.ConditionTrue,
					Reason:  "OperatorGroupMatches",
					Message: "the OperatorGroup matches what is required by the policy",
				},
				"the OperatorGroup required by the policy was created",
			)
		})
		It("Should become NonCompliant when an extra OperatorGroup is added", func() {
			utils.Kubectl("apply", "-f", extraOpGroupYAML, "-n", opPolTestNS)
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "OperatorGroup",
						APIVersion: "operators.coreos.com/v1",
					},
					Compliant: "NonCompliant",
					Reason:    "There is more than one OperatorGroup in this namespace",
				}, {
					Object: policyv1.ObjectResource{
						Kind:       "OperatorGroup",
						APIVersion: "operators.coreos.com/v1",
						Metadata: policyv1.ObjectMetadata{
							Name:      extraOpGroupName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "NonCompliant",
					Reason:    "There is more than one OperatorGroup in this namespace",
				}},
				metav1.Condition{
					Type:    "OperatorGroupCompliant",
					Status:  metav1.ConditionFalse,
					Reason:  "TooManyOperatorGroups",
					Message: "there is more than one OperatorGroup in the namespace",
				},
				"there is more than one OperatorGroup in the namespace",
			)
		})
		It("Should warn about the OperatorGroup when it doesn't match the default", func() {
			utils.Kubectl("patch", "operatorpolicy", opPolName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/remediationAction", "value": "inform"}]`)
			utils.Kubectl("delete", "operatorgroup", "-n", opPolTestNS, "--all")
			utils.Kubectl("apply", "-f", extraOpGroupYAML, "-n", opPolTestNS)
			check(
				opPolName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "OperatorGroup",
						APIVersion: "operators.coreos.com/v1",
					},
					Compliant: "Compliant",
					Reason:    "Resource found as expected",
				}},
				metav1.Condition{
					Type:   "OperatorGroupCompliant",
					Status: metav1.ConditionTrue,
					Reason: "PreexistingOperatorGroupFound",
					Message: "the policy does not specify an OperatorGroup but one already exists in the namespace" +
						" - assuming that OperatorGroup is correct",
				},
				"assuming that OperatorGroup is correct",
			)
		})
	})
	Describe("Testing OperatorGroup behavior when it is specified in the policy", Ordered, func() {
		const (
			opPolYAML            = "../resources/case38_operator_install/operator-policy-with-group.yaml"
			opPolName            = "oppol-with-group"
			incorrectOpGroupYAML = "../resources/case38_operator_install/incorrect-operator-group.yaml"
			incorrectOpGroupName = "incorrect-operator-group"
			scopedOpGroupYAML    = "../resources/case38_operator_install/scoped-operator-group.yaml"
			scopedOpGroupName    = "scoped-operator-group"
			extraOpGroupYAML     = "../resources/case38_operator_install/extra-operator-group.yaml"
			extraOpGroupName     = "extra-operator-group"
		)

		BeforeAll(func() {
			utils.Kubectl("create", "ns", opPolTestNS)
			DeferCleanup(func() {
				utils.Kubectl("delete", "ns", opPolTestNS)
			})

			utils.Kubectl("apply", "-f", incorrectOpGroupYAML, "-n", opPolTestNS)

			createObjWithParent(parentPolicyYAML, parentPolicyName,
				opPolYAML, opPolTestNS, gvrPolicy, gvrOperatorPolicy)
		})

		It("Should initially be NonCompliant", func() {
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "OperatorGroup",
						APIVersion: "operators.coreos.com/v1",
						Metadata: policyv1.ObjectMetadata{
							Name:      incorrectOpGroupName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "NonCompliant",
					Reason:    "Resource found but does not match",
				}, {
					Object: policyv1.ObjectResource{
						Kind:       "OperatorGroup",
						APIVersion: "operators.coreos.com/v1",
						Metadata: policyv1.ObjectMetadata{
							Name:      scopedOpGroupName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "NonCompliant",
					Reason:    "Resource not found but should exist",
				}},
				metav1.Condition{
					Type:    "OperatorGroupCompliant",
					Status:  metav1.ConditionFalse,
					Reason:  "OperatorGroupMismatch",
					Message: "the OperatorGroup found on the cluster does not match the policy",
				},
				"the OperatorGroup found on the cluster does not match the policy",
			)
		})
		It("Should match when the OperatorGroup is manually corrected", func() {
			utils.Kubectl("delete", "operatorgroup", incorrectOpGroupName, "-n", opPolTestNS)
			utils.Kubectl("apply", "-f", scopedOpGroupYAML, "-n", opPolTestNS)
			check(
				opPolName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "OperatorGroup",
						APIVersion: "operators.coreos.com/v1",
						Metadata: policyv1.ObjectMetadata{
							Name:      scopedOpGroupName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "Compliant",
					Reason:    "Resource found as expected",
				}},
				metav1.Condition{
					Type:    "OperatorGroupCompliant",
					Status:  metav1.ConditionTrue,
					Reason:  "OperatorGroupMatches",
					Message: "the OperatorGroup matches what is required by the policy",
				},
				"the OperatorGroup matches what is required by the policy",
			)
		})
		It("Should report a mismatch when the OperatorGroup is manually edited", func() {
			utils.Kubectl("patch", "operatorgroup", scopedOpGroupName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/targetNamespaces", "value": []}]`)
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "OperatorGroup",
						APIVersion: "operators.coreos.com/v1",
						Metadata: policyv1.ObjectMetadata{
							Name:      scopedOpGroupName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "NonCompliant",
					Reason:    "Resource found but does not match",
				}},
				metav1.Condition{
					Type:    "OperatorGroupCompliant",
					Status:  metav1.ConditionFalse,
					Reason:  "OperatorGroupMismatch",
					Message: "the OperatorGroup found on the cluster does not match the policy",
				},
				"the OperatorGroup found on the cluster does not match the policy",
			)
		})
		It("Should update the OperatorGroup when it is changed to enforce", func() {
			utils.Kubectl("patch", "operatorpolicy", opPolName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/remediationAction", "value": "enforce"}]`)
			check(
				opPolName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "OperatorGroup",
						APIVersion: "operators.coreos.com/v1",
						Metadata: policyv1.ObjectMetadata{
							Name:      scopedOpGroupName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "Compliant",
					Reason:    "Resource found as expected",
				}},
				metav1.Condition{
					Type:    "OperatorGroupCompliant",
					Status:  metav1.ConditionTrue,
					Reason:  "OperatorGroupMatches",
					Message: "the OperatorGroup matches what is required by the policy",
				},
				"the OperatorGroup was updated to match the policy",
			)
		})
		It("Should become NonCompliant when an extra OperatorGroup is added", func() {
			utils.Kubectl("apply", "-f", extraOpGroupYAML, "-n", opPolTestNS)
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "OperatorGroup",
						APIVersion: "operators.coreos.com/v1",
					},
					Compliant: "NonCompliant",
					Reason:    "There is more than one OperatorGroup in this namespace",
				}, {
					Object: policyv1.ObjectResource{
						Kind:       "OperatorGroup",
						APIVersion: "operators.coreos.com/v1",
						Metadata: policyv1.ObjectMetadata{
							Name:      extraOpGroupName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "NonCompliant",
					Reason:    "There is more than one OperatorGroup in this namespace",
				}},
				metav1.Condition{
					Type:    "OperatorGroupCompliant",
					Status:  metav1.ConditionFalse,
					Reason:  "TooManyOperatorGroups",
					Message: "there is more than one OperatorGroup in the namespace",
				},
				"there is more than one OperatorGroup in the namespace",
			)
		})
	})
	Describe("Testing Subscription behavior for musthave mode while enforcing", Ordered, func() {
		const (
			opPolYAML = "../resources/case38_operator_install/operator-policy-no-group.yaml"
			opPolName = "oppol-no-group"
			subName   = "project-quay"
		)

		BeforeAll(func() {
			utils.Kubectl("create", "ns", opPolTestNS)
			DeferCleanup(func() {
				utils.Kubectl("delete", "ns", opPolTestNS)
			})

			createObjWithParent(parentPolicyYAML, parentPolicyName,
				opPolYAML, opPolTestNS, gvrPolicy, gvrOperatorPolicy)
		})

		It("Should initially be NonCompliant", func() {
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "Subscription",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Name:      subName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "NonCompliant",
					Reason:    "Resource not found but should exist",
				}},
				metav1.Condition{
					Type:    "SubscriptionCompliant",
					Status:  metav1.ConditionFalse,
					Reason:  "SubscriptionMissing",
					Message: "the Subscription required by the policy was not found",
				},
				"the Subscription required by the policy was not found",
			)
		})
		It("Should create the Subscription when enforced", func() {
			utils.Kubectl("patch", "operatorpolicy", opPolName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/remediationAction", "value": "enforce"}]`)
			check(
				opPolName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "Subscription",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Name:      subName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "Compliant",
					Reason:    "Resource found as expected",
				}},
				metav1.Condition{
					Type:    "SubscriptionCompliant",
					Status:  metav1.ConditionTrue,
					Reason:  "SubscriptionMatches",
					Message: "the Subscription matches what is required by the policy",
				},
				"the Subscription required by the policy was created",
			)
		})
		It("Should apply an update to the Subscription", func() {
			utils.Kubectl("patch", "operatorpolicy", opPolName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/subscription/sourceNamespace", "value": "fake"}]`)
			check(
				opPolName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "Subscription",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Name:      subName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "NonCompliant",
					Reason:    "ConstraintsNotSatisfiable",
				}},
				metav1.Condition{
					Type:   "SubscriptionCompliant",
					Status: metav1.ConditionFalse,
					Reason: "ConstraintsNotSatisfiable",
					Message: "no operators found from catalog operatorhubio-catalog in namespace fake " +
						"referenced by subscription project-quay",
				},
				"the Subscription was updated to match the policy",
			)
		})
	})
	Describe("Testing Subscription behavior for musthave mode while informing", Ordered, func() {
		const (
			opPolYAML = "../resources/case38_operator_install/operator-policy-no-group.yaml"
			opPolName = "oppol-no-group"
			subName   = "project-quay"
			subYAML   = "../resources/case38_operator_install/subscription.yaml"
		)

		BeforeAll(func() {
			utils.Kubectl("create", "ns", opPolTestNS)
			DeferCleanup(func() {
				utils.Kubectl("delete", "ns", opPolTestNS)
			})

			utils.Kubectl("apply", "-f", subYAML, "-n", opPolTestNS)

			createObjWithParent(parentPolicyYAML, parentPolicyName,
				opPolYAML, opPolTestNS, gvrPolicy, gvrOperatorPolicy)
		})
		It("Should initially notice the matching Subscription", func() {
			check(
				opPolName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "Subscription",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Name:      subName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "Compliant",
					Reason:    "Resource found as expected",
				}},
				metav1.Condition{
					Type:    "SubscriptionCompliant",
					Status:  metav1.ConditionTrue,
					Reason:  "SubscriptionMatches",
					Message: "the Subscription matches what is required by the policy",
				},
				"the Subscription matches what is required by the policy",
			)
		})
		It("Should notice the mismatch when the spec is changed in the policy", func() {
			utils.Kubectl("patch", "operatorpolicy", opPolName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/subscription/sourceNamespace", "value": "fake"}]`)
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "Subscription",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Name:      subName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "NonCompliant",
					Reason:    "Resource found but does not match",
				}},
				metav1.Condition{
					Type:    "SubscriptionCompliant",
					Status:  metav1.ConditionFalse,
					Reason:  "SubscriptionMismatch",
					Message: "the Subscription found on the cluster does not match the policy",
				},
				"the Subscription found on the cluster does not match the policy",
			)
		})
	})
	Describe("Test health checks on OLM resources after OperatorPolicy operator installation", Ordered, func() {
		const (
			opPolYAML        = "../resources/case38_operator_install/operator-policy-no-group-enforce.yaml"
			opPolName        = "oppol-no-group-enforce"
			opPolNoExistYAML = "../resources/case38_operator_install/operator-policy-no-exist-enforce.yaml"
			opPolNoExistName = "oppol-no-exist-enforce"
		)
		BeforeAll(func() {
			utils.Kubectl("create", "ns", opPolTestNS)
			DeferCleanup(func() {
				utils.Kubectl("delete", "ns", opPolTestNS)
			})

			createObjWithParent(parentPolicyYAML, parentPolicyName,
				opPolYAML, opPolTestNS, gvrPolicy, gvrOperatorPolicy)

			createObjWithParent(parentPolicyYAML, parentPolicyName,
				opPolNoExistYAML, opPolTestNS, gvrPolicy, gvrOperatorPolicy)
		})

		It("Should generate conditions and relatedobjects of CSV", func(ctx SpecContext) {
			Eventually(func(ctx SpecContext) string {
				csv, _ := clientManagedDynamic.Resource(gvrClusterServiceVersion).Namespace(opPolTestNS).
					Get(ctx, "quay-operator.v3.8.13", metav1.GetOptions{})

				if csv == nil {
					return ""
				}

				reason, _, _ := unstructured.NestedString(csv.Object, "status", "reason")

				return reason
			}, olmWaitTimeout, 5, ctx).Should(Equal("InstallSucceeded"))

			check(
				opPolName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "ClusterServiceVersion",
						APIVersion: "operators.coreos.com/v1alpha1",
					},
					Compliant: "Compliant",
					Reason:    "InstallSucceeded",
				}},
				metav1.Condition{
					Type:    "ClusterServiceVersionCompliant",
					Status:  metav1.ConditionTrue,
					Reason:  "InstallSucceeded",
					Message: "ClusterServiceVersion - install strategy completed with no errors",
				},
				"ClusterServiceVersion - install strategy completed with no errors",
			)
		})

		It("Should generate conditions and relatedobjects of Deployments", func() {
			check(
				opPolName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "Deployment",
						APIVersion: "apps/v1",
					},
					Compliant: "Compliant",
					Reason:    "Deployment Available",
				}},
				metav1.Condition{
					Type:    "DeploymentCompliant",
					Status:  metav1.ConditionTrue,
					Reason:  "DeploymentsAvailable",
					Message: "All operator Deployments have their minimum availability",
				},
				"All operator Deployments have their minimum availability",
			)
		})

		It("Should only be noncompliant if the subscription error relates to the one in the operator policy", func() {
			By("Checking that " + opPolNoExistName + " is NonCompliant")
			check(
				opPolNoExistName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "Subscription",
						APIVersion: "operators.coreos.com/v1alpha1",
					},
					Compliant: "NonCompliant",
					Reason:    "ConstraintsNotSatisfiable",
				}},
				metav1.Condition{
					Type:   "SubscriptionCompliant",
					Status: metav1.ConditionFalse,
					Reason: "ConstraintsNotSatisfiable",
					Message: "constraints not satisfiable: no operators found in package project-quay-does-not-exist" +
						" in the catalog referenced by subscription project-quay-does-not-exist, subscription " +
						"project-quay-does-not-exist exists",
				},
				"constraints not satisfiable: no operators found in package project-quay-does-not-exist",
			)

			// Check if the subscription is still compliant on the operator policy trying to install a valid operator.
			// This tests that subscription status filtering is working properly since OLM includes the
			// subscription errors as a condition on all subscriptions in the namespace.
			By("Checking that " + opPolName + " is still Compliant and unaffected by " + opPolNoExistName)
			check(
				opPolName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "Subscription",
						APIVersion: "operators.coreos.com/v1alpha1",
					},
					Compliant: "Compliant",
					Reason:    "Resource found as expected",
				}},
				metav1.Condition{
					Type:    "SubscriptionCompliant",
					Status:  metav1.ConditionTrue,
					Reason:  "SubscriptionMatches",
					Message: "the Subscription matches what is required by the policy",
				},
				"the Subscription matches what is required by the policy",
			)
		})
	})
	Describe("Test health checks on OLM resources on OperatorPolicy with failed CSV", Ordered, func() {
		const (
			opPolYAML = "../resources/case38_operator_install/operator-policy-no-group-csv-fail.yaml"
			opPolName = "oppol-no-allnamespaces"
		)
		BeforeAll(func() {
			utils.Kubectl("create", "ns", opPolTestNS)
			DeferCleanup(func() {
				utils.Kubectl("delete", "ns", opPolTestNS)
			})

			createObjWithParent(parentPolicyYAML, parentPolicyName,
				opPolYAML, opPolTestNS, gvrPolicy, gvrOperatorPolicy)
		})

		It("Should generate conditions and relatedobjects of CSV", func(ctx SpecContext) {
			Eventually(func(ctx SpecContext) []unstructured.Unstructured {
				csvList, _ := clientManagedDynamic.Resource(gvrClusterServiceVersion).Namespace(opPolTestNS).
					List(ctx, metav1.ListOptions{})

				return csvList.Items
			}, olmWaitTimeout, 5, ctx).ShouldNot(BeEmpty())

			check(
				opPolName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "ClusterServiceVersion",
						APIVersion: "operators.coreos.com/v1alpha1",
					},
					Compliant: "NonCompliant",
					Reason:    "UnsupportedOperatorGroup",
				}},
				metav1.Condition{
					Type:   "ClusterServiceVersionCompliant",
					Status: metav1.ConditionFalse,
					Reason: "UnsupportedOperatorGroup",
					Message: "ClusterServiceVersion - AllNamespaces InstallModeType not supported," +
						" cannot configure to watch all namespaces",
				},
				"ClusterServiceVersion - AllNamespaces InstallModeType not supported,"+
					" cannot configure to watch all namespaces",
			)
		})

		It("Should generate conditions and relatedobjects of Deployments", func() {
			check(
				opPolName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "Deployment",
						APIVersion: "apps/v1",
					},
					Compliant: "NonCompliant",
					Reason:    "Resource not found but should exist",
				}},
				metav1.Condition{
					Type:    "DeploymentCompliant",
					Status:  metav1.ConditionTrue,
					Reason:  "NoExistingDeployments",
					Message: "No existing operator Deployments",
				},
				"No existing operator Deployments",
			)
		})

		It("Should not have any compliant events", func() {
			// This test is meant to find an incorrect compliant event that is emitted between some
			// correct noncompliant events.
			events := utils.GetMatchingEvents(
				clientManaged, opPolTestNS, parentPolicyName, "", "^Compliant;", eventuallyTimeout,
			)

			Expect(events).To(BeEmpty())
		})
	})
	Describe("Test status reporting for CatalogSource", Ordered, func() {
		const (
			OpPlcYAML  = "../resources/case38_operator_install/operator-policy-with-group.yaml"
			OpPlcName  = "oppol-with-group"
			subName    = "project-quay"
			catSrcName = "operatorhubio-catalog"
		)

		BeforeAll(func() {
			By("Applying creating a ns and the test policy")
			utils.Kubectl("create", "ns", opPolTestNS)
			DeferCleanup(func() {
				utils.Kubectl("patch", "catalogsource", catSrcName, "-n", "olm", "--type=json", "-p",
					`[{"op": "replace", "path": "/spec/image", "value": "quay.io/operatorhubio/catalog:latest"}]`)
				utils.Kubectl("delete", "ns", opPolTestNS)
			})

			createObjWithParent(parentPolicyYAML, parentPolicyName,
				OpPlcYAML, opPolTestNS, gvrPolicy, gvrOperatorPolicy)
		})

		It("Should initially show the CatalogSource is compliant", func() {
			By("Checking the condition fields")
			check(
				OpPlcName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "CatalogSource",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Name:      catSrcName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "Compliant",
					Reason:    "Resource found as expected",
				}},
				metav1.Condition{
					Type:    "CatalogSourcesUnhealthy",
					Status:  metav1.ConditionFalse,
					Reason:  "CatalogSourcesFound",
					Message: "CatalogSource was found",
				},
				"CatalogSource was found",
			)
		})
		It("Should remain compliant when policy is enforced", func() {
			By("Enforcing the policy")
			utils.Kubectl("patch", "operatorpolicy", OpPlcName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/remediationAction", "value": "enforce"}]`)

			By("Checking the condition fields")
			check(
				OpPlcName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "CatalogSource",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Name:      catSrcName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "Compliant",
					Reason:    "Resource found as expected",
				}},
				metav1.Condition{
					Type:    "CatalogSourcesUnhealthy",
					Status:  metav1.ConditionFalse,
					Reason:  "CatalogSourcesFound",
					Message: "CatalogSource was found",
				},
				"CatalogSource was found",
			)
		})
		It("Should become NonCompliant when CatalogSource DNE", func() {
			By("Patching the policy to reference a CatalogSource that DNE to emulate failure")
			utils.Kubectl("patch", "operatorpolicy", OpPlcName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/subscription/source", "value": "fakeName"}]`)

			By("Checking the conditions and relatedObj in the policy")
			check(
				OpPlcName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "CatalogSource",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Name:      "fakeName",
							Namespace: opPolTestNS,
						},
					},
					Compliant: "NonCompliant",
					Reason:    "Resource not found but should exist",
				}},
				metav1.Condition{
					Type:    "CatalogSourcesUnhealthy",
					Status:  metav1.ConditionTrue,
					Reason:  "CatalogSourcesNotFound",
					Message: "CatalogSource 'fakeName' was not found",
				},
				"CatalogSource 'fakeName' was not found",
			)
		})
		It("Should remain NonCompliant when CatalogSource fails", func() {
			By("Patching the policy to point to an existing CatalogSource")
			utils.Kubectl("patch", "operatorpolicy", OpPlcName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/subscription/source", "value": "operatorhubio-catalog"}]`)

			By("Patching the CatalogSource to reference a broken image link")
			utils.Kubectl("patch", "catalogsource", catSrcName, "-n", "olm", "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/image", "value": "quay.io/operatorhubio/fakecatalog:latest"}]`)

			By("Checking the conditions and relatedObj in the policy")
			check(
				OpPlcName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "CatalogSource",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Name:      catSrcName,
							Namespace: opPolTestNS,
						},
					},
					Compliant: "NonCompliant",
					Reason:    "Resource found as expected but is unhealthy",
				}},
				metav1.Condition{
					Type:    "CatalogSourcesUnhealthy",
					Status:  metav1.ConditionTrue,
					Reason:  "CatalogSourcesFoundUnhealthy",
					Message: "CatalogSource was found but is unhealthy",
				},
				"CatalogSource was found but is unhealthy",
			)
		})
	})
	Describe("Testing InstallPlan approval and status behavior", Ordered, func() {
		const (
			opPolYAML = "../resources/case38_operator_install/operator-policy-manual-upgrades.yaml"
			opPolName = "oppol-manual-upgrades"
			subName   = "strimzi-kafka-operator"
		)

		var (
			firstInstallPlanName  string
			secondInstallPlanName string
		)

		BeforeAll(func() {
			utils.Kubectl("create", "ns", opPolTestNS)
			DeferCleanup(func() {
				utils.Kubectl("delete", "ns", opPolTestNS)
			})

			createObjWithParent(parentPolicyYAML, parentPolicyName,
				opPolYAML, opPolTestNS, gvrPolicy, gvrOperatorPolicy)
		})

		It("Should initially report the ConstraintsNotSatisfiable Subscription", func(ctx SpecContext) {
			Eventually(func(ctx SpecContext) interface{} {
				sub, _ := clientManagedDynamic.Resource(gvrSubscription).Namespace(opPolTestNS).
					Get(ctx, subName, metav1.GetOptions{})

				if sub == nil {
					return ""
				}

				conditions, _, _ := unstructured.NestedSlice(sub.Object, "status", "conditions")
				for _, cond := range conditions {
					condMap, ok := cond.(map[string]interface{})
					if !ok {
						continue
					}

					condType, _, _ := unstructured.NestedString(condMap, "type")
					if condType == "ResolutionFailed" {
						return condMap["status"]
					}
				}

				return nil
			}, olmWaitTimeout, 5, ctx).Should(Equal("True"))
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "Subscription",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Namespace: opPolTestNS,
							Name:      subName,
						},
					},
					Compliant: "NonCompliant",
					Reason:    "ConstraintsNotSatisfiable",
				}},
				metav1.Condition{
					Type:   "SubscriptionCompliant",
					Status: metav1.ConditionFalse,
					Reason: "ConstraintsNotSatisfiable",
					Message: "no operators found with name strimzi-cluster-operator.v0.0.0.1337 in channel " +
						"strimzi-0.36.x of package strimzi-kafka-operator in the catalog referenced by " +
						"subscription strimzi-kafka-operator",
				},
				"constraints not satisfiable",
			)
		})
		It("Should initially report that no InstallPlans are found", func() {
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "InstallPlan",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Namespace: opPolTestNS,
							Name:      "-",
						},
					},
					Compliant: "Compliant",
					Reason:    "There are no relevant InstallPlans in this namespace",
				}},
				metav1.Condition{
					Type:    "InstallPlanCompliant",
					Status:  metav1.ConditionTrue,
					Reason:  "NoInstallPlansFound",
					Message: "there are no relevant InstallPlans in the namespace",
				},
				"there are no relevant InstallPlans in the namespace",
			)
		})
		It("Should report an available upgrade", func(ctx SpecContext) {
			goodVersion := "strimzi-cluster-operator.v0.36.0"
			utils.Kubectl("patch", "operatorpolicy", opPolName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/subscription/startingCSV", "value": "`+goodVersion+`"},`+
					`{"op": "replace", "path": "/spec/remediationAction", "value": "inform"}]`)
			utils.Kubectl("patch", "subscription.operator", subName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/startingCSV", "value": "`+goodVersion+`"}]`)
			Eventually(func(ctx SpecContext) int {
				ipList, _ := clientManagedDynamic.Resource(gvrInstallPlan).Namespace(opPolTestNS).
					List(ctx, metav1.ListOptions{})

				return len(ipList.Items)
			}, olmWaitTimeout, 5, ctx).Should(Equal(1))
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "InstallPlan",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Namespace: opPolTestNS,
						},
					},
					Compliant: "NonCompliant",
					Reason:    "The InstallPlan is RequiresApproval",
				}},
				metav1.Condition{
					Type:    "InstallPlanCompliant",
					Status:  metav1.ConditionFalse,
					Reason:  "InstallPlanRequiresApproval",
					Message: "an InstallPlan to update to [strimzi-cluster-operator.v0.36.0] is available for approval",
				},
				"an InstallPlan to update .* is available for approval",
			)
		})
		It("Should do the upgrade when enforced, and stop at the next version", func(ctx SpecContext) {
			ipList, err := clientManagedDynamic.Resource(gvrInstallPlan).Namespace(opPolTestNS).
				List(ctx, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ipList.Items).To(HaveLen(1))

			firstInstallPlanName = ipList.Items[0].GetName()

			utils.Kubectl("patch", "operatorpolicy", opPolName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/remediationAction", "value": "enforce"}]`)

			Eventually(func(ctx SpecContext) int {
				ipList, err = clientManagedDynamic.Resource(gvrInstallPlan).Namespace(opPolTestNS).
					List(ctx, metav1.ListOptions{})

				return len(ipList.Items)
			}, olmWaitTimeout, 5, ctx).Should(Equal(2))

			secondInstallPlanName = ipList.Items[1].GetName()
			if firstInstallPlanName == secondInstallPlanName {
				secondInstallPlanName = ipList.Items[0].GetName()
			}

			Eventually(func(ctx SpecContext) string {
				ip, _ := clientManagedDynamic.Resource(gvrInstallPlan).Namespace(opPolTestNS).
					Get(ctx, firstInstallPlanName, metav1.GetOptions{})
				phase, _, _ := unstructured.NestedString(ip.Object, "status", "phase")

				return phase
			}, olmWaitTimeout, 5, ctx).Should(Equal("Complete"))

			// This check covers several situations that occur quickly: the first InstallPlan eventually
			// progresses to Complete after it is approved, and the next InstallPlan is created and
			// recognized by the policy (but not yet approved).
			check(
				opPolName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "InstallPlan",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Namespace: opPolTestNS,
							Name:      firstInstallPlanName,
						},
					},
					Reason: "The InstallPlan is Complete",
				}, {
					Object: policyv1.ObjectResource{
						Kind:       "InstallPlan",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Namespace: opPolTestNS,
							Name:      secondInstallPlanName,
						},
					},
					Compliant: "NonCompliant",
					Reason:    "The InstallPlan is RequiresApproval",
				}},
				metav1.Condition{
					Type:   "InstallPlanCompliant",
					Status: metav1.ConditionFalse,
					Reason: "InstallPlanRequiresApproval",
					Message: "an InstallPlan to update to [strimzi-cluster-operator.v0.36.1] is available for " +
						"approval but not allowed by the specified versions in the policy",
				},
				"the InstallPlan.*36.0.*was approved",
			)
		})
		It("Should approve the next version when it's added to the spec", func(ctx SpecContext) {
			utils.Kubectl("patch", "operatorpolicy", opPolName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "add", "path": "/spec/versions/-", "value": "strimzi-cluster-operator.v0.36.1"}]`)

			Eventually(func(ctx SpecContext) string {
				ip, _ := clientManagedDynamic.Resource(gvrInstallPlan).Namespace(opPolTestNS).
					Get(ctx, secondInstallPlanName, metav1.GetOptions{})
				phase, _, _ := unstructured.NestedString(ip.Object, "status", "phase")

				return phase
			}, olmWaitTimeout, 5, ctx).Should(Equal("Complete"))

			check(
				opPolName,
				false,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "InstallPlan",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Namespace: opPolTestNS,
							Name:      firstInstallPlanName,
						},
					},
					Reason: "The InstallPlan is Complete",
				}, {
					Object: policyv1.ObjectResource{
						Kind:       "InstallPlan",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Namespace: opPolTestNS,
							Name:      secondInstallPlanName,
						},
					},
					Reason: "The InstallPlan is Complete",
				}},
				metav1.Condition{
					Type:    "InstallPlanCompliant",
					Status:  metav1.ConditionTrue,
					Reason:  "NoInstallPlansRequiringApproval",
					Message: "no InstallPlans requiring approval were found",
				},
				"the InstallPlan.*36.1.*was approved",
			)
		})
	})
	Describe("Testing OperatorPolicy validation messages", Ordered, func() {
		const (
			opPolYAML = "../resources/case38_operator_install/operator-policy-validity-test.yaml"
			opPolName = "oppol-validity-test"
			subName   = "project-quay"
		)

		BeforeAll(func() {
			utils.Kubectl("create", "ns", opPolTestNS)
			DeferCleanup(func() {
				utils.Kubectl("delete", "ns", opPolTestNS)
				utils.Kubectl("delete", "ns", "nonexist-testns")
			})

			createObjWithParent(parentPolicyYAML, parentPolicyName,
				opPolYAML, opPolTestNS, gvrPolicy, gvrOperatorPolicy)
		})

		It("Should initially report unknown fields", func() {
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{},
				metav1.Condition{
					Type:    "ValidPolicySpec",
					Status:  metav1.ConditionFalse,
					Reason:  "InvalidPolicySpec",
					Message: `spec.subscription is invalid: json: unknown field "actually"`,
				},
				`the status of the Subscription could not be determined because the policy is invalid`,
			)
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{},
				metav1.Condition{
					Type:    "ValidPolicySpec",
					Status:  metav1.ConditionFalse,
					Reason:  "InvalidPolicySpec",
					Message: `spec.operatorGroup is invalid: json: unknown field "foo"`,
				},
				`the status of the OperatorGroup could not be determined because the policy is invalid`,
			)
		})
		It("Should report about the invalid installPlanApproval value", func() {
			// remove the "unknown" fields
			utils.Kubectl("patch", "operatorpolicy", opPolName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "remove", "path": "/spec/operatorGroup/foo"}, `+
					`{"op": "remove", "path": "/spec/subscription/actually"}]`)
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{},
				metav1.Condition{
					Type:   "ValidPolicySpec",
					Status: metav1.ConditionFalse,
					Reason: "InvalidPolicySpec",
					Message: "spec.subscription.installPlanApproval ('Incorrect') is invalid: " +
						"must be 'Automatic' or 'Manual'",
				},
				"NonCompliant",
			)
		})
		It("Should report about the namespaces not matching", func() {
			// Fix the `installPlanApproval` value
			utils.Kubectl("patch", "operatorpolicy", opPolName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "replace", "path": "/spec/subscription/installPlanApproval", "value": "Automatic"}]`)
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{},
				metav1.Condition{
					Type:   "ValidPolicySpec",
					Status: metav1.ConditionFalse,
					Reason: "InvalidPolicySpec",
					Message: "the namespace specified in spec.operatorGroup ('operator-policy-testns') must match " +
						"the namespace used for the subscription ('nonexist-testns')",
				},
				"NonCompliant",
			)
		})
		It("Should report about the namespace not existing", func() {
			// Fix the namespace mismatch by removing the operator group spec
			utils.Kubectl("patch", "operatorpolicy", opPolName, "-n", opPolTestNS, "--type=json", "-p",
				`[{"op": "remove", "path": "/spec/operatorGroup"}]`)
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "Subscription",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Name:      subName,
							Namespace: "nonexist-testns",
						},
					},
					Compliant: "NonCompliant",
					Reason:    "Resource not found but should exist",
				}},
				metav1.Condition{
					Type:    "ValidPolicySpec",
					Status:  metav1.ConditionFalse,
					Reason:  "InvalidPolicySpec",
					Message: "the operator namespace ('nonexist-testns') does not exist",
				},
				"NonCompliant",
			)
		})
		It("Should update the status after the namespace is created", func() {
			utils.Kubectl("create", "namespace", "nonexist-testns")
			check(
				opPolName,
				true,
				[]policyv1.RelatedObject{{
					Object: policyv1.ObjectResource{
						Kind:       "Subscription",
						APIVersion: "operators.coreos.com/v1alpha1",
						Metadata: policyv1.ObjectMetadata{
							Name:      subName,
							Namespace: "nonexist-testns",
						},
					},
					Compliant: "NonCompliant",
					Reason:    "Resource not found but should exist",
				}},
				metav1.Condition{
					Type:    "ValidPolicySpec",
					Status:  metav1.ConditionTrue,
					Reason:  "PolicyValidated",
					Message: "the policy spec is valid",
				},
				"the policy spec is valid",
			)
		})
	})
})
