package integration_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paketo-buildpacks/occam"
	"github.com/sclevine/spec"

	. "github.com/onsi/gomega"
	. "github.com/paketo-buildpacks/occam/matchers"
)

func testLayerReuse(t *testing.T, context spec.G, it spec.S) {
	var (
		Expect       = NewWithT(t).Expect
		Eventually   = NewWithT(t).Eventually
		pack         occam.Pack
		docker       occam.Docker
		imageIDs     map[string]struct{}
		containerIDs map[string]struct{}
	)

	it.Before(func() {
		pack = occam.NewPack()
		docker = occam.NewDocker()
		imageIDs = map[string]struct{}{}
		containerIDs = map[string]struct{}{}
	})

	context("when an app is rebuilt with no changes", func() {
		var (
			firstImage      occam.Image
			secondImage     occam.Image
			secondContainer occam.Container
			name            string
			source          string
		)

		it.Before(func() {
			var err error
			name, err = occam.RandomName()
			Expect(err).NotTo(HaveOccurred())
		})

		it.After(func() {
			for containerID := range containerIDs {
				Expect(docker.Container.Remove.Execute(containerID)).To(Succeed())
			}

			for imageID := range imageIDs {
				Expect(docker.Image.Remove.Execute(imageID)).To(Succeed())
			}

			Expect(docker.Volume.Remove.Execute(occam.CacheVolumeNames(name))).To(Succeed())
			Expect(os.RemoveAll(source)).To(Succeed())
		})

		it("reuses the cached runtime layer", func() {
			var err error
			source, err = occam.Source(filepath.Join("testdata", "default"))
			Expect(err).NotTo(HaveOccurred())

			var logs fmt.Stringer
			firstImage, logs, err = pack.WithNoColor().Build.
				WithPullPolicy("never").
				WithBuildpacks(
					settings.Buildpacks.DotnetCoreRuntime.Online,
					settings.Buildpacks.BuildPlan.Online,
				).
				Execute(name, source)
			Expect(err).NotTo(HaveOccurred(), logs.String())

			imageIDs[firstImage.ID] = struct{}{}

			Expect(firstImage.Buildpacks).To(HaveLen(2))
			Expect(firstImage.Buildpacks[0].Key).To(Equal(settings.BuildpackInfo.Buildpack.ID))
			Expect(firstImage.Buildpacks[0].Layers).To(HaveKey("dotnet-core-runtime"))

			// second pack build

			secondImage, logs, err = pack.WithNoColor().Build.
				WithPullPolicy("never").
				WithBuildpacks(
					settings.Buildpacks.DotnetCoreRuntime.Online,
					settings.Buildpacks.BuildPlan.Online,
				).
				Execute(name, source)
			Expect(err).NotTo(HaveOccurred(), logs.String())

			imageIDs[secondImage.ID] = struct{}{}

			Expect(secondImage.Buildpacks).To(HaveLen(2))
			Expect(secondImage.Buildpacks[0].Key).To(Equal(settings.BuildpackInfo.Buildpack.ID))
			Expect(secondImage.Buildpacks[0].Layers).To(HaveKey("dotnet-core-runtime"))

			Expect(logs).To(ContainLines(
				"  Resolving Dotnet Core Runtime version",
				"    Candidate version sources (in priority order):",
				"      <unknown> -> \"\"",
				"",
				MatchRegexp(`    Selected dotnet-runtime version \(using <unknown>\): \d+\.\d+\.\d+`),
				MatchRegexp(`      Version 5\.\d+\.\d+ of dotnet-runtime will be deprecated after 2022-05-08.`),
				"      Migrate your application to a supported version of dotnet-runtime before this time.",
				"",
				MatchRegexp(fmt.Sprintf("  Reusing cached layer /layers/%s/dotnet-core-runtime", strings.ReplaceAll(settings.BuildpackInfo.Buildpack.ID, "/", "_"))),
				"",
			))
			secondContainer, err = docker.Container.Run.
				WithCommand("ls -al $DOTNET_ROOT && ls -al $DOTNET_ROOT/shared").
				Execute(secondImage.ID)
			Expect(err).NotTo(HaveOccurred())

			containerIDs[secondContainer.ID] = struct{}{}

			Eventually(func() string {
				cLogs, err := docker.Container.Logs.Execute(secondContainer.ID)
				Expect(err).NotTo(HaveOccurred())
				return cLogs.String()
			}).Should(
				And(
					MatchRegexp(fmt.Sprintf(`.* \d+ cnb cnb   \d+ .* host -> \/layers\/%s\/dotnet-core-runtime\/host`, strings.ReplaceAll(settings.BuildpackInfo.Buildpack.ID, "/", "_"))),
					MatchRegexp(`.* \d+ cnb cnb \d+ .* shared`),
					MatchRegexp(fmt.Sprintf(`.* \d+ cnb cnb   \d+ .* Microsoft.NETCore.App -> \/layers\/%s\/dotnet-core-runtime\/shared\/Microsoft.NETCore.App`, strings.ReplaceAll(settings.BuildpackInfo.Buildpack.ID, "/", "_"))),
				),
			)

			Expect(secondImage.Buildpacks[0].Layers["dotnet-core-runtime"].SHA).To(Equal(firstImage.Buildpacks[0].Layers["dotnet-core-runtime"].SHA))
		})
	})

	context("when an app is rebuilt with changed requirements", func() {
		var (
			firstImage        occam.Image
			secondImage       occam.Image
			secondContainer   occam.Container
			name              string
			source            string
			availableVersion1 string
			availableVersion2 string
		)

		it.Before(func() {
			var err error
			name, err = occam.RandomName()
			Expect(err).NotTo(HaveOccurred())
			availableVersion1 = settings.BuildpackInfo.Metadata.Dependencies[0].Version
			availableVersion2 = settings.BuildpackInfo.Metadata.Dependencies[1].Version
		})

		it.After(func() {
			for containerID := range containerIDs {
				Expect(docker.Container.Remove.Execute(containerID)).To(Succeed())
			}

			for imageID := range imageIDs {
				Expect(docker.Image.Remove.Execute(imageID)).To(Succeed())
			}

			Expect(docker.Volume.Remove.Execute(occam.CacheVolumeNames(name))).To(Succeed())
			Expect(os.RemoveAll(source)).To(Succeed())
		})

		it("does not reuse the cached runtime layer", func() {
			var err error
			source, err = occam.Source(filepath.Join("testdata", "default"))
			Expect(err).NotTo(HaveOccurred())

			var logs fmt.Stringer
			err = os.WriteFile(filepath.Join(source, "plan.toml"), []byte(fmt.Sprintf(`[[requires]]
			name = "dotnet-runtime"

				[requires.metadata]
					launch = true
					version = "%s"
			`, availableVersion1)), os.ModePerm)
			Expect(err).NotTo(HaveOccurred())
			firstImage, logs, err = pack.WithNoColor().Build.
				WithPullPolicy("never").
				WithBuildpacks(
					settings.Buildpacks.DotnetCoreRuntime.Online,
					settings.Buildpacks.BuildPlan.Online,
				).
				Execute(name, source)
			Expect(err).NotTo(HaveOccurred(), logs.String())

			imageIDs[firstImage.ID] = struct{}{}

			Expect(firstImage.Buildpacks).To(HaveLen(2))
			Expect(firstImage.Buildpacks[0].Key).To(Equal(settings.BuildpackInfo.Buildpack.ID))
			Expect(firstImage.Buildpacks[0].Layers).To(HaveKey("dotnet-core-runtime"))

			// second pack build
			err = os.WriteFile(filepath.Join(source, "plan.toml"), []byte(fmt.Sprintf(`[[requires]]
			name = "dotnet-runtime"

				[requires.metadata]
					launch = true
					version = "%s"
			`, availableVersion2)), os.ModePerm)
			Expect(err).NotTo(HaveOccurred())

			secondImage, logs, err = pack.WithNoColor().Build.
				WithPullPolicy("never").
				WithBuildpacks(
					settings.Buildpacks.DotnetCoreRuntime.Online,
					settings.Buildpacks.BuildPlan.Online,
				).
				Execute(name, source)
			Expect(err).NotTo(HaveOccurred(), logs.String())

			imageIDs[secondImage.ID] = struct{}{}

			Expect(secondImage.Buildpacks).To(HaveLen(2))
			Expect(secondImage.Buildpacks[0].Key).To(Equal(settings.BuildpackInfo.Buildpack.ID))
			Expect(secondImage.Buildpacks[0].Layers).To(HaveKey("dotnet-core-runtime"))

			Expect(logs).To(ContainLines(
				"  Resolving Dotnet Core Runtime version",
				"    Candidate version sources (in priority order):",
				MatchRegexp(`      <unknown> -> "\d+\.\d+\.\d+"`),
				"",
				MatchRegexp(`    Selected dotnet-runtime version \(using <unknown>\): \d+\.\d+\.\d+`),
			))

			Expect(logs).NotTo(ContainSubstring("Reusing cached layer"))

			secondContainer, err = docker.Container.Run.
				WithCommand("ls -al $DOTNET_ROOT && ls -al $DOTNET_ROOT/shared").
				Execute(secondImage.ID)
			Expect(err).NotTo(HaveOccurred())

			containerIDs[secondContainer.ID] = struct{}{}

			Eventually(func() string {
				cLogs, err := docker.Container.Logs.Execute(secondContainer.ID)
				Expect(err).NotTo(HaveOccurred())
				return cLogs.String()
			}).Should(
				And(
					MatchRegexp(fmt.Sprintf(`.* \d+ cnb cnb   \d+ .* host -> \/layers\/%s\/dotnet-core-runtime\/host`, strings.ReplaceAll(settings.BuildpackInfo.Buildpack.ID, "/", "_"))),
					MatchRegexp(`.* \d+ cnb cnb \d+ .* shared`),
					MatchRegexp(fmt.Sprintf(`.* \d+ cnb cnb   \d+ .* Microsoft.NETCore.App -> \/layers\/%s\/dotnet-core-runtime\/shared\/Microsoft.NETCore.App`, strings.ReplaceAll(settings.BuildpackInfo.Buildpack.ID, "/", "_"))),
				),
			)

			Expect(secondImage.Buildpacks[0].Layers["dotnet-core-runtime"].SHA).NotTo(Equal(firstImage.Buildpacks[0].Layers["dotnet-core-runtime"].SHA))
		})
	})
}
