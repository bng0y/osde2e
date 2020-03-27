package e2e

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/Masterminds/semver"
	"github.com/openshift/osde2e/pkg/common/config"
	"github.com/openshift/osde2e/pkg/common/metadata"
	"github.com/openshift/osde2e/pkg/common/osd"
	"github.com/openshift/osde2e/pkg/common/state"
	"github.com/openshift/osde2e/pkg/common/upgrade"
)

func filterOnCincinnati(version *semver.Version) bool {
	versionInCincinnati, err := upgrade.IsVersionInCincinnati(version)

	if err != nil {
		log.Printf("error while trying to filter on version in Cincinnati: %v", err)
		return false
	}

	return versionInCincinnati
}

// ChooseVersions sets versions in cfg if not set based on defaults and upgrade options.
// If a release stream is set for an upgrade the previous available version is used and it's image is used for upgrade.
func ChooseVersions(osdClient *osd.OSD) (err error) {
	state := state.Instance

	// when defined, use set version
	if len(state.Cluster.Version) != 0 {
		err = nil
	} else if osdClient == nil {
		err = errors.New("osd must be setup when upgrading with release stream")
	} else if shouldUpgrade() {
		err = setupUpgradeVersion(osdClient)
	} else {
		err = setupVersion(osdClient)
	}

	// Set the versions in metadata. If upgrade hasn't been chosen, it should still be omitted from the end result.
	metadata.Instance.SetClusterVersion(state.Cluster.Version)
	metadata.Instance.SetUpgradeVersion(state.Upgrade.ReleaseName)

	return err
}

// shouldUpgrade determines if this test run should attempt an upgrade.
func shouldUpgrade() bool {
	cfg := config.Instance
	state := state.Instance

	return state.Upgrade.Image == "" &&
		(cfg.Upgrade.ReleaseStream != "" ||
			cfg.Upgrade.UpgradeToCISIfPossible ||
			cfg.Upgrade.NextReleaseAfterProdDefaultForUpgrade > -1)
}

// chooses between default version and nightly based on target versions.
func setupVersion(osdClient *osd.OSD) (err error) {
	cfg := config.Instance
	state := state.Instance
	suffix := ""

	if len(state.Cluster.Version) == 0 && (cfg.Cluster.MajorTarget != 0 || cfg.Cluster.MinorTarget != 0) {
		majorTarget := cfg.Cluster.MajorTarget
		// don't require major to be set
		if majorTarget == 0 {
			majorTarget = -1
		}

		if cfg.OCM.Env == "int" && cfg.Upgrade.ReleaseStream == "" {
			suffix = "nightly"
		}

		// look for the latest release and install it for this OSD cluster.
		if state.Cluster.Version, err = osdClient.LatestVersionWithTarget(majorTarget, cfg.Cluster.MinorTarget, suffix); err == nil {
			log.Printf("CLUSTER_VERSION not set but a TARGET is, running '%s'", state.Cluster.Version)
		}
	}

	if len(state.Cluster.Version) == 0 {
		var err error
		var versionType string
		if cfg.Cluster.UseLatestVersionForInstall {
			state.Cluster.Version, err = osdClient.LatestVersion()
			versionType = "latest version"
		} else if cfg.Cluster.UseMiddleClusterImageSetForInstall {
			state.Cluster.Version, state.Cluster.EnoughVersionsForOldestOrMiddleTest, err = osdClient.MiddleVersion()
			versionType = "middle version"
		} else if cfg.Cluster.UseOldestClusterImageSetForInstall {
			state.Cluster.Version, state.Cluster.EnoughVersionsForOldestOrMiddleTest, err = osdClient.OldestVersion()
			versionType = "oldest version"
		} else if cfg.Cluster.NextReleaseAfterProdDefault > -1 {
			state.Cluster.Version, err = osdClient.NextReleaseAfterProdDefault(cfg.Cluster.NextReleaseAfterProdDefault)
			versionType = fmt.Sprintf("%d release(s) from the default version in prod", cfg.Cluster.NextReleaseAfterProdDefault)
		} else if cfg.OCM.Env == "int" {
			state.Cluster.Version, err = osd.DefaultVersionInProd()
			versionType = "current default in prod"
		} else {
			state.Cluster.Version, err = osdClient.DefaultVersion()
			versionType = "current default"
		}

		if err == nil {
			if state.Cluster.EnoughVersionsForOldestOrMiddleTest {
				log.Printf("CLUSTER_VERSION not set, using the %s '%s'", versionType, state.Cluster.Version)
			} else {
				log.Printf("Unable to get the %s.", versionType)
			}
		} else {
			return fmt.Errorf("Error finding default cluster version: %v", err)
		}
	}

	return
}

// chooses version based on optimal upgrade path
func setupUpgradeVersion(osdClient *osd.OSD) (err error) {
	cfg := config.Instance
	state := state.Instance

	// Decide the version to install
	err = setupVersion(osdClient)
	if err != nil {
		return err
	}

	clusterVersion, err := osd.OpenshiftVersionToSemver(state.Cluster.Version)
	if err != nil {
		log.Printf("error while parsing cluster version %s: %v", state.Cluster.Version, err)
		return err
	}

	if cfg.OCM.Env != "int" {
		cisUpgradeVersionString, err := osdClient.LatestVersionWithFilter(filterOnCincinnati)

		if err != nil {
			log.Printf("unable to get the most recent version of openshift from OSD: %v", err)
			return err
		}

		cisUpgradeVersion, err := osd.OpenshiftVersionToSemver(cisUpgradeVersionString)

		if err != nil {
			log.Printf("unable to parse most recent version of openshift from OSD: %v", err)
			return err
		}

		// If the available cluster image set makes sense, then we'll just use that
		if !cisUpgradeVersion.LessThan(clusterVersion) {
			log.Printf("Using cluster image set.")
			state.Upgrade.ReleaseName = cisUpgradeVersionString
			metadata.Instance.SetUpgradeVersionSource("cluster image set")
			state.Upgrade.UpgradeVersionEqualToInstallVersion = cisUpgradeVersion.Equal(clusterVersion)
			log.Printf("Selecting version '%s' to be able to upgrade to '%s'", state.Cluster.Version, state.Upgrade.ReleaseName)
			return nil
		}

		if state.Upgrade.ReleaseName != "" {
			log.Printf("The most recent cluster image set is equal to the default. Falling back to upgrading with Cincinnati.")
		} else {
			return fmt.Errorf("couldn't get latest cluster image set release and no Cincinnati fallback")
		}
	}

	releaseStream := cfg.Upgrade.ReleaseStream

	if releaseStream == "" {
		if cfg.Upgrade.NextReleaseAfterProdDefaultForUpgrade > -1 {
			nextVersion, err := osdClient.NextReleaseAfterProdDefault(cfg.Upgrade.NextReleaseAfterProdDefaultForUpgrade)

			if err != nil {
				return fmt.Errorf("error determining next version to upgrade to: %v", err)
			}

			nextVersionSemver, err := osd.OpenshiftVersionToSemver(nextVersion)
			if err != nil {
				return fmt.Errorf("error parsing version returned as production default: %v", err)
			}

			releaseStream = fmt.Sprintf("%d.%d.0-0.nightly", nextVersionSemver.Major(), nextVersionSemver.Minor())
		} else {
			return fmt.Errorf("no release stream specified and no dynamic version selection specified")
		}
	}

	state.Upgrade.ReleaseName, state.Upgrade.Image, err = upgrade.LatestReleaseFromReleaseController(releaseStream)
	if err != nil {
		return fmt.Errorf("couldn't get latest release from release-controller: %v", err)
	}

	upgradeVersion, err := osd.OpenshiftVersionToSemver(state.Upgrade.ReleaseName)
	if err != nil {
		log.Printf("error while parsing upgrade version %s: %v", state.Upgrade.ReleaseName, err)
		return err
	}

	if !clusterVersion.LessThan(upgradeVersion) && !strings.Contains(state.Upgrade.ReleaseName, "nightly") {
		log.Printf("Cluster version is equal to or newer than the upgrade version. Looking up previous version...")
		if state.Cluster.Version, err = osdClient.PreviousVersion(state.Upgrade.ReleaseName); err != nil {
			return fmt.Errorf("failed retrieving previous version to '%s': %v", state.Upgrade.ReleaseName, err)
		}
	}

	// set upgrade image
	log.Printf("Selecting version '%s' to be able to upgrade to '%s' on release stream '%s'",
		state.Cluster.Version, state.Upgrade.ReleaseName, releaseStream)
	return
}

// GetEnabledNoDefaultVersions returns a sorted list of the enabled but not default versions currently offered by OSD
func GetEnabledNoDefaultVersions() ([]string, error) {
	cfg := config.Instance

	OSD, err := osd.New(cfg.OCM.Token, cfg.OCM.Env, cfg.OCM.Debug)
	if err != nil {
		return nil, fmt.Errorf("could not setup OSD: %v", err)
	}

	return OSD.EnabledNoDefaultVersionList()
}
