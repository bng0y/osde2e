package verify

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/osde2e/pkg/common/helper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	annotationName  = "cookie-same-site"
	samesiteSetting = "Strict"
	monNamespace    = "openshift-monitoring"
	conNamespace    = "openshift-console"
	supportVersion  = 46 // samesite cookie is only supported on >= v4.6.x
)

var samesiteTestName string = "[Suite: informing] [OSD] Samesite Cookie Strict"

var _ = ginkgo.Describe(samesiteTestName, func() {
	h := helper.New()

	ginkgo.Context("Validating samesite cookie", func() {

		checkVersion := verifyVersion(h)

		ginkgo.FIt("should be set for openshift-monitoring OSD managed routes", func() {
			if checkVersion() {
				ginkgo.Skip("skipping due to unsupported cluster version. Must be >=4.6.0")
			}
			foundKey, err := managedRoutes(h, monNamespace)
			Expect(err).NotTo(HaveOccurred(), "failed getting routes for %v", monNamespace)
			Expect(foundKey).Should(BeTrue(), "%v namespace routes have samesite cookie set", monNamespace)
		}, 5)

		ginkgo.FIt("should be set for openshift-console OSD managed routes", func() {
			if checkVersion() {
				ginkgo.Skip("skipping due to unsupported cluster version. Must be >=4.6.0")
			}
			foundKey, err := managedRoutes(h, conNamespace)
			Expect(err).NotTo(HaveOccurred(), "failed getting routes for %v", conNamespace)
			Expect(foundKey).Should(BeTrue(), "%v namespace routes have samesite cookie set", conNamespace)
		}, 5)
	})
})

func verifyVersion(h *helper.H) func() bool {
	return func() bool {
		unsupportedVersion := false
		clusterVersionObj, err := h.GetClusterVersion()
		Expect(err).NotTo(HaveOccurred(), "failed getting cluster version")
		Expect(clusterVersionObj).NotTo(BeNil())

		// Get the cluster version and slice it, then convert the major/minor version to int Ex. majMinVersion := 46
		splitVersion := strings.Split(clusterVersionObj.Status.Desired.Version, ".")

		// Assume the len is <= 3 since semver major/minor could be something like 4.11.x
		if len(splitVersion) != 2 && len(splitVersion) != 3 {
			return true
		}

		//if len(splitVersion) > 3 && len(splitVersion) != 0 {
		//	return true
		//}

		majMinVersion, err := strconv.Atoi(splitVersion[0] + splitVersion[1])
		Expect(err).NotTo(HaveOccurred(), "failed normalizing major/minor version to integer %v", err)

		// Even if the semver maj/min exceed 4.9, this will still work. Ex. 4.11 = 411, which 411 > 46
		if majMinVersion < supportVersion {
			unsupportedVersion = true
		}
		return unsupportedVersion
	}
}

func managedRoutes(h *helper.H, namespace string) (bool, error) {
	route, err := h.Route().RouteV1().Routes(namespace).List(context.TODO(), metav1.ListOptions{})
	samesiteExists := false
	if err != nil || route == nil {
		return false, fmt.Errorf("failed requesting routes: %v", err)
	}

	for key, value := range route.Items[0].Annotations {
		if strings.Contains(key, annotationName) && strings.Contains(value, samesiteSetting) {
			samesiteExists = true
		}
	}
	return samesiteExists, nil
}
