package dotnetcoreruntime

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/Masterminds/semver"
	"github.com/paketo-buildpacks/packit"
	"github.com/paketo-buildpacks/packit/postal"
)

type RuntimeVersionResolver struct{}

func NewRuntimeVersionResolver() RuntimeVersionResolver {
	return RuntimeVersionResolver{}
}

func (r RuntimeVersionResolver) Resolve(path string, entry packit.BuildpackPlanEntry, stack string, logger LogEmitter) (postal.Dependency, error) {
	var buildpackTOML struct {
		Metadata struct {
			Dependencies []postal.Dependency `toml:"dependencies"`
		} `toml:"metadata"`
	}

	_, err := toml.DecodeFile(path, &buildpackTOML)
	if err != nil {
		return postal.Dependency{}, err
	}

	version := ""
	_, ok := entry.Metadata["version"]
	if ok {
		version = entry.Metadata["version"].(string)
	}

	if version == "" || version == "default" {
		version = "*"
	}

	runtimeConstraint, err := semver.NewConstraint(version)
	if err != nil {
		return postal.Dependency{}, err
	}
	constraints := []semver.Constraints{*runtimeConstraint}

	versionSource := ""
	_, ok = entry.Metadata["version-source"]
	if ok {
		versionSource = entry.Metadata["version-source"].(string)
	}

	// If the version source is buildpack.yml, we will only look for the version itself
	// Only do roll forward logic for other version sources.
	if versionSource != "buildpack.yml" {
		// Check to see if the version given is a semantic version. If it is not like
		// "*" then there would be a failure in parsing. Anything that is a
		// non-semver we try and form a constraint and use that as the sole
		// constraint.
		splitVersion := strings.Split(version, ".")
		if len(splitVersion) == 3 && splitVersion[len(splitVersion)-1] != "*" {
			runtimeVersion, err := semver.NewVersion(version)
			if err != nil {
				return postal.Dependency{}, err
			}

			minorConstraint, err := semver.NewConstraint(fmt.Sprintf("%d.%d.*", runtimeVersion.Major(), runtimeVersion.Minor()))
			if err != nil {
				return postal.Dependency{}, err
			}
			constraints = append(constraints, *minorConstraint)

			majorConstraint, err := semver.NewConstraint(fmt.Sprintf("%d.*", runtimeVersion.Major()))
			if err != nil {
				return postal.Dependency{}, err
			}
			constraints = append(constraints, *majorConstraint)
		}
	}

	var supportedVersions []string
	var filteredDependencies []postal.Dependency
	var id = entry.Name
	for _, dependency := range buildpackTOML.Metadata.Dependencies {
		if dependency.ID == id && containsStack(dependency.Stacks, stack) {
			filteredDependencies = append(filteredDependencies, dependency)
			supportedVersions = append(supportedVersions, dependency.Version)
		}
	}

	var compatibleDependencies []postal.Dependency
	for i, constraint := range constraints {
		if i == 1 {
			logger.Subprocess("No exact version match found; attempting version roll-forward")
			logger.Break()
		}
		for _, dependency := range filteredDependencies {
			sVersion, err := semver.NewVersion(dependency.Version)
			if err != nil {
				return postal.Dependency{}, err
			}

			if constraint.Check(sVersion) {
				compatibleDependencies = append(compatibleDependencies, dependency)
			}
		}

		// on first constraint iteration, this is what stops on an exact match
		if len(compatibleDependencies) > 0 {
			break
		}
	}

	if len(compatibleDependencies) == 0 {
		return postal.Dependency{}, fmt.Errorf(
			"failed to satisfy %q dependency for stack %q with version constraint %q: no compatible versions. Supported versions are: [%s]",
			id,
			stack,
			version,
			strings.Join(supportedVersions, ", "),
		)
	}

	sort.Slice(compatibleDependencies, func(i, j int) bool {
		iVersion := semver.MustParse(compatibleDependencies[i].Version)
		jVersion := semver.MustParse(compatibleDependencies[j].Version)
		return iVersion.GreaterThan(jVersion)
	})

	return compatibleDependencies[0], nil
}

func containsStack(stacks []string, stack string) bool {
	for _, s := range stacks {
		if s == stack {
			return true
		}
	}
	return false
}
