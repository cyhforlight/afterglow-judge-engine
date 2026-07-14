package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"afterglow-judge-engine/internal/execution"
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

type language interface {
	Resolve(model.Language) (languageToolchain, error)
}

type languageToolchain interface {
	Compile(context.Context, string) (compiledProgram, model.CompileResult, error)
}

type compiledProgram interface {
	Run(context.Context, string, int, int) (RunResult, error)
}

type languageEngine struct {
	compiler Compiler
	runner   Runner
}

type resolvedLanguage struct {
	language model.Language
	profile  languageProfile
	compiler Compiler
	runner   Runner
}

type compiledLanguageProgram struct {
	language model.Language
	profile  runConfig
	artifact model.CompiledArtifact
	runner   Runner
}

type languageProfile struct {
	Compile compileConfig
	Run     runConfig
}

type compileConfig struct {
	ImageRef     string
	SourceFile   string
	ArtifactName string
	BuildCommand []string
	TimeoutMs    int
	MemoryMB     int
}

type runConfig struct {
	ImageRef       string
	ArtifactName   string
	RuntimeCommand func(artifactPath string, memoryMB int) []string
}

func newLanguage(compiler Compiler, runner Runner) language {
	return &languageEngine{compiler: compiler, runner: runner}
}

func (l *languageEngine) Resolve(lang model.Language) (languageToolchain, error) {
	profile, err := profileForLanguage(lang)
	if err != nil {
		return nil, err
	}
	return &resolvedLanguage{
		language: lang,
		profile:  profile,
		compiler: l.compiler,
		runner:   l.runner,
	}, nil
}

func (l *resolvedLanguage) Compile(
	ctx context.Context,
	sourceCode string,
) (compiledProgram, model.CompileResult, error) {
	config := l.profile.Compile
	compileOut, err := l.compiler.Compile(ctx, CompileRequest{
		Files: []execution.File{{
			Name:    config.SourceFile,
			Content: []byte(sourceCode),
			Mode:    0o644,
		}},
		ImageRef:     config.ImageRef,
		Command:      config.BuildCommand,
		ArtifactName: config.ArtifactName,
		Limits: execution.Limits{
			CPUTimeMs:   config.TimeoutMs,
			WallTimeMs:  config.TimeoutMs * execution.WallTimeMultiplier,
			MemoryMB:    config.MemoryMB,
			OutputBytes: execution.DefaultCompileOutputLimitBytes,
		},
	})
	if err != nil {
		return nil, model.CompileResult{}, err
	}
	if !compileOut.Result.Succeeded {
		return nil, compileOut.Result, nil
	}
	if compileOut.Artifact == nil {
		return nil, model.CompileResult{}, errors.New("compile succeeded but artifact is missing")
	}

	return &compiledLanguageProgram{
		language: l.language,
		profile:  l.profile.Run,
		artifact: *compileOut.Artifact,
		runner:   l.runner,
	}, compileOut.Result, nil
}

func (p *compiledLanguageProgram) Run(
	ctx context.Context,
	input string,
	timeLimitMs int,
	memoryLimitMB int,
) (RunResult, error) {
	if len(p.artifact.Data) == 0 {
		return RunResult{}, errors.New("program artifact is required")
	}

	containerPath := runMountDir + "/" + p.profile.ArtifactName
	runOut, err := p.runner.Run(ctx, RunRequest{
		Files: []execution.File{{
			Name:    p.profile.ArtifactName,
			Content: p.artifact.Data,
			Mode:    p.artifact.Mode,
		}},
		ImageRef: p.profile.ImageRef,
		Command:  p.profile.RuntimeCommand(containerPath, memoryLimitMB),
		Stdin:    strings.NewReader(input),
		Limits: execution.Limits{
			CPUTimeMs:   timeLimitMs,
			WallTimeMs:  timeLimitMs * execution.WallTimeMultiplier,
			MemoryMB:    sandboxMemoryLimitMB(p.language, memoryLimitMB),
			OutputBytes: execution.DefaultRunOutputLimitBytes,
		},
	})
	if err != nil {
		return RunResult{}, err
	}

	return normalizeLanguageRunResult(p.language, runOut), nil
}

func profileForLanguage(lang model.Language) (languageProfile, error) {
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
		return languageProfile{}, fmt.Errorf("unsupported language: %v", lang)
	}
}

func cProfile() languageProfile {
	return languageProfile{
		Compile: compileConfig{
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
		Run: runConfig{
			ImageRef:       staticRuntimeImage,
			ArtifactName:   defaultArtifactName,
			RuntimeCommand: func(p string, _ int) []string { return []string{p} },
		},
	}
}

func cppProfile() languageProfile {
	return languageProfile{
		Compile: compileConfig{
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
		Run: runConfig{
			ImageRef:       staticRuntimeImage,
			ArtifactName:   defaultArtifactName,
			RuntimeCommand: func(p string, _ int) []string { return []string{p} },
		},
	}
}

func javaProfile() languageProfile {
	return languageProfile{
		Compile: compileConfig{
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
		Run: runConfig{
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

func pythonProfile() languageProfile {
	return languageProfile{
		Compile: compileConfig{
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
		Run: runConfig{
			ImageRef:       pythonImage,
			ArtifactName:   "solution.pyc",
			RuntimeCommand: func(p string, _ int) []string { return []string{"python3", p} },
		},
	}
}

func sandboxMemoryLimitMB(lang model.Language, memoryLimitMB int) int {
	if lang != model.LanguageJava {
		return memoryLimitMB
	}
	return memoryLimitMB + max(javaNativeReserveMB, memoryLimitMB/4)
}

func normalizeLanguageRunResult(lang model.Language, runOut RunResult) RunResult {
	if lang == model.LanguageJava &&
		runOut.Verdict == execution.VerdictRE &&
		strings.Contains(runOut.Stderr, "java.lang.OutOfMemoryError") {
		runOut.Verdict = execution.VerdictMLE
	}
	return runOut
}
