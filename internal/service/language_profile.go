package service

import (
	"fmt"
	"strconv"

	"afterglow-judge-engine/internal/model"
)

const (
	gccImage            = "docker.io/library/gcc:12-bookworm"
	staticRuntimeImage  = "docker.io/library/debian:12-slim"
	pythonImage         = "docker.io/library/python:3.11-slim-bookworm"
	defaultArtifactName = "program"
	javaNativeReserveMB = 64
	optimizationFlag    = "-O2"
	pipeFlag            = "-pipe"
	staticLinkFlag      = "-static"
	mathLibraryFlag     = "-lm"
)

// LanguageProfile groups the compile-time and run-time settings for a language.
// It keeps language-specific images, filenames, and commands in one place.
type LanguageProfile struct {
	Compile CompileConfig
	Run     RunConfig
}

// CompileConfig describes how to compile source code in a container.
type CompileConfig struct {
	ImageRef     string
	SourceFile   string
	ArtifactName string
	BuildCommand []string
	TimeoutMs    int
	MemoryMB     int
}

// RunConfig describes how to execute a compiled artifact in a container.
type RunConfig struct {
	ImageRef       string
	ArtifactName   string
	RuntimeCommand func(artifactPath string, memoryMB int) []string
}

// ProfileForLanguage returns the language profile for the given language.
func ProfileForLanguage(lang model.Language) (LanguageProfile, error) {
	switch lang {
	case model.LanguageC:
		return cProfile(), nil
	case model.LanguageCPP:
		return cppProfile(), nil
	case model.LanguageJava:
		return javaProfile(), nil
	case model.LanguagePython:
		return pythonProfile(), nil
	default:
		return LanguageProfile{}, fmt.Errorf("unsupported language: %v", lang)
	}
}

// cProfile returns the profile for C language.
func cProfile() LanguageProfile {
	return LanguageProfile{
		Compile: CompileConfig{
			ImageRef:     gccImage,
			SourceFile:   "main.c",
			ArtifactName: defaultArtifactName,
			BuildCommand: []string{
				"gcc", optimizationFlag, pipeFlag, staticLinkFlag, "-s",
				"-o", compileMountDir + "/" + defaultArtifactName,
				compileMountDir + "/main.c", mathLibraryFlag,
			},
			TimeoutMs: 30000,
			MemoryMB:  512,
		},
		Run: RunConfig{
			ImageRef:       staticRuntimeImage,
			ArtifactName:   defaultArtifactName,
			RuntimeCommand: func(p string, _ int) []string { return []string{p} },
		},
	}
}

// cppProfile returns the profile for C++ language.
func cppProfile() LanguageProfile {
	return LanguageProfile{
		Compile: CompileConfig{
			ImageRef:     gccImage,
			SourceFile:   "main.cpp",
			ArtifactName: defaultArtifactName,
			BuildCommand: []string{
				"g++", "-std=c++20", optimizationFlag, pipeFlag, staticLinkFlag, "-s",
				"-o", compileMountDir + "/" + defaultArtifactName,
				compileMountDir + "/main.cpp", mathLibraryFlag,
			},
			TimeoutMs: 30000,
			MemoryMB:  512,
		},
		Run: RunConfig{
			ImageRef:       staticRuntimeImage,
			ArtifactName:   defaultArtifactName,
			RuntimeCommand: func(p string, _ int) []string { return []string{p} },
		},
	}
}

// javaProfile returns the profile for Java language.
func javaProfile() LanguageProfile {
	return LanguageProfile{
		Compile: CompileConfig{
			ImageRef:     "docker.io/library/eclipse-temurin:21-jdk-jammy",
			SourceFile:   "Main.java",
			ArtifactName: "solution.jar",
			BuildCommand: []string{
				"sh", "-c",
				"mkdir -p " + compileMountDir + "/classes && " +
					"javac -encoding UTF-8 -d " + compileMountDir + "/classes " + compileMountDir + "/Main.java && " +
					"jar --create --file " + compileMountDir + "/solution.jar --main-class Main -C " + compileMountDir + "/classes .",
			},
			TimeoutMs: 30000,
			MemoryMB:  512,
		},
		Run: RunConfig{
			ImageRef:     "docker.io/library/eclipse-temurin:21-jre-jammy",
			ArtifactName: "solution.jar",
			RuntimeCommand: func(p string, memoryMB int) []string {
				initialHeapMB := min(memoryMB, 64)
				return []string{
					"java",
					"-Xmx" + strconv.Itoa(memoryMB) + "m",
					"-Xms" + strconv.Itoa(initialHeapMB) + "m",
					"-jar",
					p,
				}
			},
		},
	}
}

func sandboxMemoryLimitMB(lang model.Language, memoryLimitMB int) int {
	if lang != model.LanguageJava {
		return memoryLimitMB
	}
	return memoryLimitMB + max(javaNativeReserveMB, memoryLimitMB/4)
}

// pythonProfile returns the profile for Python language.
// Python compiles to bytecode (.pyc) to catch syntax errors early.
func pythonProfile() LanguageProfile {
	return LanguageProfile{
		Compile: CompileConfig{
			ImageRef:     pythonImage,
			SourceFile:   "solution.py",
			ArtifactName: "solution.pyc",
			BuildCommand: []string{
				"sh", "-c",
				"python3 -c 'import py_compile; py_compile.compile(\"" + compileMountDir + "/solution.py\", cfile=\"" + compileMountDir + "/solution.pyc\", doraise=True)' || exit 1",
			},
			TimeoutMs: 10000,
			MemoryMB:  256,
		},
		Run: RunConfig{
			ImageRef:       pythonImage,
			ArtifactName:   "solution.pyc",
			RuntimeCommand: func(p string, _ int) []string { return []string{"python3", p} },
		},
	}
}
