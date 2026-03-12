package service

import (
	"fmt"
	"os"

	"afterglow-judge-engine/internal/model"
)

// LanguageProfile defines compilation and execution configuration for a language.
// This encapsulates language-specific build commands and runtime parameters,
// fulfilling the role of "Business Logic Layer 2 (Task Encapsulation)" as defined in PRD 3.3.
type LanguageProfile struct {
	Compile CompileConfig
	Run     RunConfig
}

// CompileConfig describes how to compile source code in a container.
type CompileConfig struct {
	ImageRef     string
	SourceFiles  []string
	ArtifactName string
	BuildCommand func(sources []string) []string
	TimeoutMs    int
	MemoryMB     int
}

// RunConfig describes how to execute a compiled artifact in a container.
type RunConfig struct {
	ImageRef       string
	ArtifactName   string
	RuntimeCommand func(artifactPath string) []string
	FileMode       os.FileMode
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
			ImageRef:     "docker.io/library/gcc:12-bookworm",
			SourceFiles:  []string{"main.c"},
			ArtifactName: "program",
			BuildCommand: func(sources []string) []string {
				args := make([]string, 0, 9+len(sources))
				args = append(args, "gcc", "-O2", "-pipe", "-static", "-s", "-o", "/work/program")
				for _, src := range sources {
					args = append(args, "/work/"+src)
				}
				args = append(args, "-lm")
				return args
			},
			TimeoutMs: 30000,
			MemoryMB:  512,
		},
		Run: RunConfig{
			ImageRef:       "gcr.io/distroless/static-debian12:latest",
			ArtifactName:   "program",
			RuntimeCommand: func(p string) []string { return []string{p} },
			FileMode:       0755,
		},
	}
}

// cppProfile returns the profile for C++ language.
func cppProfile() LanguageProfile {
	return LanguageProfile{
		Compile: CompileConfig{
			ImageRef:     "docker.io/library/gcc:12-bookworm",
			SourceFiles:  []string{"main.cpp"},
			ArtifactName: "program",
			BuildCommand: func(sources []string) []string {
				args := make([]string, 0, 11+len(sources))
				args = append(args, "g++", "-std=c++20", "-O2", "-pipe", "-static", "-s", "-o", "/work/program")
				for _, src := range sources {
					args = append(args, "/work/"+src)
				}
				args = append(args, "-lm")
				return args
			},
			TimeoutMs: 30000,
			MemoryMB:  512,
		},
		Run: RunConfig{
			ImageRef:       "gcr.io/distroless/static-debian12:latest",
			ArtifactName:   "program",
			RuntimeCommand: func(p string) []string { return []string{p} },
			FileMode:       0755,
		},
	}
}

// checkerProfile returns the profile for checker compilation and execution.
// Checkers are C++ programs that use testlib.h to validate test outputs.
func checkerProfile() LanguageProfile {
	return LanguageProfile{
		Compile: CompileConfig{
			ImageRef:     "docker.io/library/gcc:12-bookworm",
			SourceFiles:  []string{"checker.cpp"},
			ArtifactName: "checker",
			BuildCommand: func(sources []string) []string {
				args := make([]string, 0, 11+len(sources))
				args = append(args, "g++", "-std=c++20", "-O2", "-pipe", "-static", "-s", "-o", "/work/checker")
				for _, src := range sources {
					args = append(args, "/work/"+src)
				}
				args = append(args, "-lm")
				return args
			},
			TimeoutMs: 30000,
			MemoryMB:  512,
		},
		Run: RunConfig{
			ImageRef:       "gcr.io/distroless/static-debian12:latest",
			ArtifactName:   "checker",
			RuntimeCommand: func(p string) []string { return []string{p} },
			FileMode:       0755,
		},
	}
}

// javaProfile returns the profile for Java language.
func javaProfile() LanguageProfile {
	return LanguageProfile{
		Compile: CompileConfig{
			ImageRef:     "docker.io/library/eclipse-temurin:21-jdk-jammy",
			SourceFiles:  []string{"Main.java"},
			ArtifactName: "solution.jar",
			BuildCommand: func(_ []string) []string {
				return []string{"sh", "-c",
					"mkdir -p /work/classes && " +
						"javac -encoding UTF-8 -d /work/classes /work/Main.java && " +
						"jar --create --file /work/solution.jar --main-class Main -C /work/classes ."}
			},
			TimeoutMs: 30000,
			MemoryMB:  512,
		},
		Run: RunConfig{
			ImageRef:       "gcr.io/distroless/java21-debian12:latest",
			ArtifactName:   "solution.jar",
			RuntimeCommand: func(p string) []string { return []string{"java", "-Xmx256m", "-Xms64m", "-jar", p} },
			FileMode:       0644,
		},
	}
}

// pythonProfile returns the profile for Python language.
// Python compiles to bytecode (.pyc) to catch syntax errors early.
func pythonProfile() LanguageProfile {
	return LanguageProfile{
		Compile: CompileConfig{
			ImageRef:     "docker.io/library/python:3.11-slim-bookworm",
			SourceFiles:  []string{"solution.py"},
			ArtifactName: "solution.pyc",
			BuildCommand: func(_ []string) []string {
				return []string{
					"sh", "-c",
					"python3 -c 'import py_compile; py_compile.compile(\"/work/solution.py\", cfile=\"/work/solution.pyc\", doraise=True)' || exit 1",
				}
			},
			TimeoutMs: 10000,
			MemoryMB:  256,
		},
		Run: RunConfig{
			ImageRef:       "gcr.io/distroless/python3-debian12:latest",
			ArtifactName:   "solution.pyc",
			RuntimeCommand: func(p string) []string { return []string{"python3", p} },
			FileMode:       0644,
		},
	}
}
