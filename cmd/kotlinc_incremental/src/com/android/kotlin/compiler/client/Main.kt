/*
 * Copyright (C) 2025 The Android Open Source Project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package com.android.kotlin.compiler.client

import java.io.File
import java.net.URLClassLoader
import java.util.UUID
import kotlin.system.exitProcess
import org.jetbrains.kotlin.buildtools.api.CompilationService
import org.jetbrains.kotlin.buildtools.api.ExperimentalBuildToolsApi
import org.jetbrains.kotlin.buildtools.api.ProjectId
import org.jetbrains.kotlin.buildtools.api.SourcesChanges
import org.jetbrains.kotlin.buildtools.api.jvm.ClassSnapshotGranularity
import org.jetbrains.kotlin.buildtools.api.jvm.ClasspathEntrySnapshot
import org.jetbrains.kotlin.buildtools.api.jvm.ClasspathSnapshotBasedIncrementalCompilationApproachParameters

private val ARGUMENT_PARSERS =
    listOf(
        BuildDirArgument(),
        BuildFileArgument(),
        BuildHistoryFileArgument(),
        ClassPathArgument(),
        HelpArgument(),
        JvmArgument(),
        LogDirArgument(),
        OutputDirArgument(),
        PluginArgument(),
        RunFilesArgument(),
        RootDirArgument(),
        WorkingDirArgument(),
        XBuildFileArgument(),
        SourcesArgument(), // must come last
    )

fun main(args: Array<String>) {
    val opts = Options()
    ARGUMENT_PARSERS.forEach { it.setupDefault(opts) }

    if (!parseArgs(args, opts)) {
        exitProcess(-1)
    }

    if (opts.sources.isEmpty() && (opts.buildFile == null || opts.buildFileSources.isEmpty())) {
        println("No sources or build file specified. Exiting.")
        exitProcess(0)
    }

    BTACompilation(opts)
}

fun parseArgs(args: Array<String>, opts: Options): Boolean {
    var matched = false
    var hasError = false
    var showHelp = args.isEmpty()
    val iter = args.iterator()
    while (iter.hasNext()) {
        val arg = iter.next()
        matched = false
        for (parser in ARGUMENT_PARSERS) {
            if (parser.matches(arg)) {
                matched = true
                if (parser is HelpArgument) {
                    showHelp = true
                }
                parser.parse(arg, iter, opts)
                if (parser.error != null) {
                    hasError = true
                    System.err.println(parser.error)
                    System.err.println()
                }
                break
            }
        }
        if (!matched) {
            opts.passThroughArgs.add(arg.substring(0))
        }
    }

    if (showHelp) {
        showArgumentHelp()
    }

    return !hasError
}

fun showArgumentHelp() {
    var longest = -1
    val padding = 5

    println(
        "Usage: kotlin-incremental-client <-root-dir>=<dir> [options] [kotlinc options] " +
            "[-- <source files>]"
    )
    println()
    for (parser in ARGUMENT_PARSERS) {
        if (parser.argumentName.length > longest) {
            longest = parser.argumentName.length
        }
    }

    val indent = " ".repeat(longest + padding)
    for (parser in ARGUMENT_PARSERS) {
        print(("-" + parser.argumentName).padEnd(longest + padding))
        var first = true
        parser.helpText.lines().forEach {
            if (first) {
                println(it)
                first = false
            } else {
                println(indent + it)
            }
        }
        if (parser.default != null) {
            print(indent + "[Default: ")
            if (parser.default is String) {
                println("\"${parser.default}\"]")
            } else {
                println("${parser.default}]")
            }
        }
    }
}

fun BTACompilation(opts: Options) {
    val kotlincArgs = mutableListOf<String>()
    if (opts.buildFile != null) {
        if (opts.buildFileModuleName != null) {
            kotlincArgs.add("-module-name")
            kotlincArgs.add(opts.buildFileModuleName!!)
        }
    }
    kotlincArgs.add("-d=${opts.outputDir.absolutePath}")
    kotlincArgs.addAll(opts.passThroughArgs)
    kotlincArgs.addAll(opts.sources)
    kotlincArgs.addAll(opts.buildFileJavaSources)
    doBtaCompilation(
        opts.sources + opts.buildFileSources,
        opts.classPath + opts.buildFileClassPaths,
        opts.workingDir,
        kotlincArgs,
        opts.jvmArgs,
        Logger(),
    )
}

@OptIn(ExperimentalBuildToolsApi::class)
fun doBtaCompilation(
    sources: List<String>,
    classPath: List<String>,
    workingDirectory: File,
    args: List<String>,
    jvmArgs: List<String>,
    logger: Logger,
) {
    val loader =
        URLClassLoader(
            classPath.map { File(it).toURI().toURL() }.toTypedArray() +
                // TODO: don't hardcode this path.
                arrayOf(
                        "out/soong/.intermediates/external/kotlinc/kotlin-build-tools-impl/" +
                            "linux_glibc_common/local-combined/kotlin-build-tools-impl.jar"
                        // TODO: this root path also won't work on other's computers!
                    )
                    .map { File("/src/android/aosp/$it").toURI().toURL() } +

                // Need to include this code's own jar in the classpath.
                arrayOf(Options::class.java.protectionDomain?.codeSource?.location)
        )

    val service = CompilationService.loadImplementation(loader)
    val executionConfig = service.makeCompilerExecutionStrategyConfiguration()
    // TODO: investigate using the daemon.
    // Right now, it hangs (https://youtrack.jetbrains.com/issue/KT-75142/)
    // executionConfig.useDaemonStrategy(jvmArgs)
    executionConfig.useInProcessStrategy()
    val compilationConfig = service.makeJvmCompilationConfiguration()

    val cpsnapshotParameters =
        getClasspathSnapshotParameters(
            workingDirectory,
            classPath,
            service::calculateClasspathSnapshot,
        )

    val sourcesChanges =
        SourcesChanges.Known(
            modifiedFiles = listOf(sources.first()).map { File(it) },
            removedFiles = emptyList(),
        )
    val incJvmCompilationConfig =
        compilationConfig.makeClasspathSnapshotBasedIncrementalCompilationConfiguration()
    incJvmCompilationConfig.assureNoClasspathSnapshotsChanges(true)
    compilationConfig.useIncrementalCompilation(
        workingDirectory,
        sourcesChanges,
        cpsnapshotParameters,
        incJvmCompilationConfig,
    )
    compilationConfig.useLogger(logger)

    val pid = ProjectId.ProjectUUID(UUID.randomUUID())
    val mArgs = args.toMutableList()
    mArgs.add("-cp")
    mArgs.add(classPath.joinToString(":"))
    service.compileJvm(pid, executionConfig, compilationConfig, sources.map { File(it) }, mArgs)
}

@OptIn(ExperimentalBuildToolsApi::class)
fun getClasspathSnapshotParameters(
    workingDirectory: File,
    classPath: List<String>,
    calculateClasspathSnapshot: (File, ClassSnapshotGranularity) -> ClasspathEntrySnapshot,
): ClasspathSnapshotBasedIncrementalCompilationApproachParameters {
    val cps = File(workingDirectory.parentFile, "shrunk-classpath-snapshot.bin")
    val cpsFiles =
        classPath.map {
            val cpFile = File(it)
            // TODO: Consider CLASS_LEVEL snapshots of things that change infrequently.
            // CLASS_MEMBER_LEVEL
            // of everything else.
            val snName = cpFile.name.replace(".", "_") + "-snapshot.bin"
            val snf = File(workingDirectory.parentFile, snName)
            // TODO: we need to delete/regenerate the snf if the jar has changed.
            if (!snf.exists()) {
                val sn =
                    calculateClasspathSnapshot(cpFile, ClassSnapshotGranularity.CLASS_MEMBER_LEVEL)
                sn.saveSnapshot(snf)
            }
            snf
        }

    return ClasspathSnapshotBasedIncrementalCompilationApproachParameters(
        newClasspathSnapshotFiles = cpsFiles,
        shrunkClasspathSnapshot = cps,
    )
}
