package main_test

import (
	"context"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func RandomString(n int) string {
	rand.Seed(time.Now().UnixNano())

	var letters = []rune("abcdefghijklmnopqrstuvwxyz")

	s := make([]rune, n)
	for i := range s {
		s[i] = letters[rand.Intn(len(letters))]
	}
	return string(s)
}

var _ = Describe("When working with a resource group", func() {
	randomString := RandomString(12)
	resourceGroupName := "tftest-" + randomString
	storageAccountName := randomString

	gvrResourceGroup := schema.GroupVersionResource{
		Group:    "azurerm.tfb.local",
		Version:  "valpha1",
		Resource: "resource-groups",
	}

	gvrStorageAccount := schema.GroupVersionResource{
		Group:    "azurerm.tfb.local",
		Version:  "valpha1",
		Resource: "storage-accounts",
	}

	It("should allow the resource lifecycle", func() {
		By("creating the resource-group CRD")

		Expect(k8sClient).ToNot(BeNil())

		objResourceGroup := unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "azurerm.tfb.local/valpha1",
				"kind":       "resource-group",
				"metadata": map[string]interface{}{
					"name": resourceGroupName,
				},
				"spec": map[string]interface{}{
					"name":     resourceGroupName,
					"location": "westeurope",
				},
			},
		}
		_, err := k8sClient.Resource(gvrResourceGroup).Namespace("default").Create(context.TODO(), &objResourceGroup, metav1.CreateOptions{})
		Expect(err).To(BeNil())

		By("returning the resource ID")
		Eventually(func() string {
			obj, err := k8sClient.Resource(gvrResourceGroup).Namespace("default").Get(context.TODO(), resourceGroupName, metav1.GetOptions{})
			Expect(err).To(BeNil())

			status, ok := obj.Object["status"].(map[string]interface{})
			Expect(err).To(BeNil())
			if !ok {
				return ""
			}

			id := status["id"].(string)
			return id
		}, time.Second*10, time.Second*5).Should(MatchRegexp("/subscriptions/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/resourceGroups/" + resourceGroupName))

		By("creating the storage account")

		objStorageAccount := unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "azurerm.tfb.local/valpha1",
				"kind":       "storage-account",
				"metadata": map[string]interface{}{
					"name": storageAccountName,
				},
				"spec": map[string]interface{}{
					"name":                     storageAccountName,
					"resource_group_name":      resourceGroupName,
					"location":                 "westeurope",
					"account_tier":             "Standard",
					"account_replication_type": "LRS",
				},
			},
		}
		_, err = k8sClient.Resource(gvrStorageAccount).Namespace("default").Create(context.TODO(), &objStorageAccount, metav1.CreateOptions{})
		Expect(err).To(BeNil())

		By("returning the storage account ID")
		Eventually(func() string {
			obj, err := k8sClient.Resource(gvrStorageAccount).Namespace("default").Get(context.TODO(), storageAccountName, metav1.GetOptions{})
			Expect(err).To(BeNil())

			status, ok := obj.Object["status"].(map[string]interface{})
			Expect(err).To(BeNil())
			if !ok {
				return ""
			}

			id := status["id"].(string)
			return id
		}, time.Minute*3, time.Second*5).Should(Not(BeEmpty())) // TODO check id format

		By("deleting the storage account CRD")
		err = k8sClient.Resource(gvrStorageAccount).Namespace("default").Delete(context.TODO(), storageAccountName, metav1.DeleteOptions{})
		Expect(err).To(BeNil())

		Eventually(func() bool {
			_, err := k8sClient.Resource(gvrStorageAccount).Namespace("default").Get(context.TODO(), storageAccountName, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					return true
				}
				Expect(err).To(BeNil())
			}
			return false
		}, time.Second*120, time.Second*10).Should(BeTrue())

		By("deleting the resource group CRD")
		err = k8sClient.Resource(gvrResourceGroup).Namespace("default").Delete(context.TODO(), resourceGroupName, metav1.DeleteOptions{})
		Expect(err).To(BeNil())

		Eventually(func() bool {
			_, err := k8sClient.Resource(gvrResourceGroup).Namespace("default").Get(context.TODO(), resourceGroupName, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					return true
				}
				Expect(err).To(BeNil())
			}
			return false
		}, time.Second*120, time.Second*10).Should(BeTrue())

	}, 20)

})
