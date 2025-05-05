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
import javax.xml.parsers.SAXParserFactory

abstract class Argument<T> {
    abstract val argumentName: String
    abstract val helpText: String
    abstract val default: T?

    var error: String? = null
        protected set

    abstract fun matches(arg: String): Boolean

    abstract fun parse(arg: String, position: Iterator<String>, opts: Options)

    abstract fun setOption(option: T, opts: Options)

    fun setupDefault(opts: Options) {
        if (default != null) {
            setOption(default!!, opts)
        }
    }
}

abstract class NoArgument : Argument<Boolean>() {
    override val default = null

    override fun matches(arg: String) = arg == "-$argumentName"

    override fun parse(arg: String, position: Iterator<String>, opts: Options) {
        setOption(true, opts)
    }
}

abstract class SingleArgument<T> : Argument<T>() {

    override fun matches(arg: String) = arg.startsWith("-$argumentName=")

    override fun parse(arg: String, position: Iterator<String>, opts: Options) {
        val splits = arg.split("=", limit = 2)
        if (splits.size != 2 || splits[1].isEmpty()) {
            error = "Required argument not supplied for $argumentName"
            return
        }
        val value = stringToType(splits[1])
        setOption(value, opts)
    }

    abstract fun stringToType(arg: String): T
}

abstract class StringArgument : SingleArgument<String>() {
    override fun stringToType(arg: String): String {
        return arg
    }
}

class Verbose : NoArgument() {
    override val argumentName = "verbose"
    override val helpText =
        """
        Outputs additional information during compilation. Quite noisy.
        """
            .trimIndent()

    override fun setOption(option: Boolean, opts: Options) {
        opts.verbose = option
    }
}

class Debug : NoArgument() {
    override val argumentName = "debug"
    override val helpText =
        """
        Outputs additional information during compilation.
        """.trimIndent()

    override fun setOption(option: Boolean, opts: Options) {
        opts.debug = option
    }
}

class SourcesArgument : Argument<String>() {
    override val default = null
    override val argumentName = "-"
    override val helpText =
        """
        Everything after this is treated as a source file.
        """.trimIndent()

    override fun matches(arg: String) = arg == "--"

    override fun parse(arg: String, position: Iterator<String>, opts: Options) {
        position.forEachRemaining { setOption(it, opts) }
    }

    override fun setOption(option: String, opts: Options) {
        opts.addSource(option)
    }
}

class BuildFileArgument : StringArgument() {
    override val default = null

    override val argumentName = "build-file"
    override val helpText =
        """
        Build file containing sources and classpaths to be consumed by kotlinc. See
        -Xbuild-file on kotlinc.
        """
            .trimIndent()

    override fun setOption(option: String, opts: Options) {
        opts.buildFileLocation = option
        parseBuildFile(opts.buildFile!!, opts)
    }

    private fun parseBuildFile(buildFile: File, opts: Options) {
        val parser = BuildFileParser()
        val spf = SAXParserFactory.newInstance()
        val saxParser = spf.newSAXParser()
        val xmlReader = saxParser.xmlReader
        xmlReader.contentHandler = parser
        xmlReader.parse(buildFile.absolutePath)

        opts.buildFileModuleName = parser.moduleName
        opts.buildFileClassPaths = parser.classpaths
        opts.buildFileSources = parser.sources
        opts.buildFileJavaSources = parser.javaSources
        if (parser.outputDirName != null) {
            opts.outputDirName = parser.outputDirName!!
        }
    }
}

class XBuildFileArgument : StringArgument() {
    override val default = null

    override val argumentName = "Xbuild-file"
    override val helpText = """
        Deprecated: use -build-file
        """.trimIndent()

    override fun setOption(option: String, opts: Options) {
        error = "Can not parse -Xbuild-file. Please use -build-file."
    }
}

class HelpArgument : NoArgument() {
    override val argumentName = "h"

    override val helpText = """
        Outputs this help text.
        """.trimIndent()

    override fun setOption(option: Boolean, opts: Options) {}
}

abstract class WritableDirectoryArgument : StringArgument() {
    override fun setOption(option: String, opts: Options) {
        val e = isValidDirectoryForWriting(option)
        if (e != null) {
            error = "Invalid $argumentName option specified: $e"
        } else {
            setDirectory(File(option), opts)
        }
    }

    abstract fun setDirectory(dir: File, opts: Options)
}

abstract class SubdirectoryArgument : StringArgument() {
    override fun setOption(option: String, opts: Options) {
        if (option.isBlank()) {
            error = "Invalid $argumentName option specified: Must be non-empty string."
        } else if (option.contains("..")) {
            error = "Invalid $argumentName option specified: No path traversal allowed."
        } else {
            setSubDirectory(option, opts)
        }
    }

    abstract fun setSubDirectory(dir: String, opts: Options)
}

class LogDirArgument : WritableDirectoryArgument() {
    override val argumentName = "log-dir"
    override val helpText = """
        Directory to write log output to.
        """.trimIndent()
    override val default = null

    override fun setDirectory(dir: File, opts: Options) {
        opts.logDir = dir
    }
}

class RunFilesArgument : WritableDirectoryArgument() {
    override val argumentName = "run-files-path"
    override val helpText =
        """
        Local directory to place lock files and other process-specific
        metadata.
        """
            .trimIndent()
    override val default = "/tmp"

    override fun setDirectory(dir: File, opts: Options) {
        opts.runFiles = dir
    }
}

class RootDirArgument : WritableDirectoryArgument() {
    override val argumentName = "root-dir"
    override val helpText =
        """
        Base directory for the Kotlin daemon's artifacts.
        Other directories - working-dir, output-dir, and build-dir - are all relative
        to this directory.
        This option is REQUIRED.
        """
            .trimIndent()
    override val default = null

    override fun setDirectory(dir: File, opts: Options) {
        opts.rootDir = dir
    }
}

class WorkingDirArgument : SubdirectoryArgument() {
    override val argumentName = "working-dir"
    override val helpText =
        """
        Stores intermediate steps used specifically for incremental compilation.
        Must be maintained between compilation invocations to see
        incremental speed benefits.
        Relative to root-dir.
        """
            .trimIndent()
    override val default = "work"

    override fun setSubDirectory(dir: String, opts: Options) {
        opts.workingDirName = dir
    }
}

class OutputDirArgument : SubdirectoryArgument() {
    override val argumentName = "output-dir"
    override val helpText =
        """
        Where to output compiler results.
        Relative to root-dir.
        """
            .trimIndent()
    override val default = "output"

    override fun setSubDirectory(dir: String, opts: Options) {
        opts.outputDirName = dir
    }
}

class BuildDirArgument : SubdirectoryArgument() {
    override val argumentName = "build-dir"
    override val helpText =
        """
        TODO: figure out what this is. Notes say:
        "buildDir is the parent of destDir and workingDir"
        """
            .trimIndent()
    override val default = "build"

    override fun setSubDirectory(dir: String, opts: Options) {
        opts.buildDirName = dir
    }
}

class BuildHistoryFileArgument : StringArgument() {
    override val argumentName = "build-history"
    override val helpText =
        """
        Location of the build-history file used for incremental compilation.
        """
            .trimIndent()
    override val default = "build-history"

    override fun setOption(option: String, opts: Options) {
        opts.buildHistoryFileName = option
    }
}

class ClassPathArgument : StringArgument() {
    override val argumentName = "classpath"
    override val helpText =
        """
        List of directories and JAR/ZIP archives to search for user class files.
        Colon separated: "foo.jar:bar.jar"
        """
            .trimIndent()
    override val default = null

    override fun setOption(option: String, opts: Options) {
        val paths = option.split(":").filter { !it.isBlank() }
        // TODO: validate paths?
        opts.classPath.addAll(paths)
    }
}

/**
 * Intercepts the -Xplugin argument of kotlinc such that we can prepend them to the front of the
 * classpath.
 *
 * Without this, you can run into a bug where a passed in plugin can cause a plugin implementing the
 * same package+classname to be loaded from a different part of the classpath than is intended.
 */
class PluginArgument : StringArgument() {
    override val argumentName = "Xplugin"
    override val helpText =
        """
        Compiler plugins passed to kotlin. See the `-Xplugin` argument of kotlinc.
    """
            .trimIndent()
    override val default = null

    override fun setOption(option: String, opts: Options) {
        opts.classPath.addFirst(option)
        opts.passThroughArgs.add("-Xplugin=$option")
    }
}

class JvmArgument : Argument<String>() {
    override val argumentName = "-J<option>"
    override val helpText = """
        Options passed through to the JVM.
        """.trimIndent()
    override val default = null

    override fun matches(arg: String) = arg.startsWith("-J")

    override fun parse(arg: String, position: Iterator<String>, opts: Options) {
        // Strip off "-J-" so that we're left with just "<option>"
        setOption(arg.substring(3), opts)
    }

    override fun setOption(option: String, opts: Options) {
        opts.jvmArgs.add(option)
    }
}

fun isValidDirectoryForWriting(filePath: String): String? {
    try {
        val file = File(filePath)
        if (file.exists()) {
            if (!file.isDirectory) {
                return "Path exists but is not a directory"
            }
            if (!file.canWrite()) {
                return "Directory exists but is not writable"
            }
        } else if (!file.mkdirs()) {
            return "Unable to create directory"
        }

        return null // All checks passed!
    } catch (e: Exception) {
        // Handle exceptions like invalid path characters, no permissions, etc.
        return e.message
    }
}

fun isValidFilePathForWriting(filePath: String): String? {
    if (filePath.isBlank()) {
        return "Empty log-file path"
    }

    try {
        val file = File(filePath)
        val parentDir = file.parentFile ?: return "Invalid parent directory"

        if (!parentDir.exists()) {
            if (!parentDir.mkdirs()) {
                return "Unable to create parent directory"
            }
        } else if (!parentDir.isDirectory) {
            return "Parent directory is not a directory"
        } else if (!parentDir.canWrite()) {
            return "Parent directory is not writable"
        }

        if (file.exists()) {
            if (file.isDirectory) {
                return "File is a directory"
            } else if (!file.canWrite()) {
                return "File exists but is not writable"
            }
        }

        return null // All checks passed!
    } catch (e: Exception) {
        // Handle exceptions like invalid path characters, no permissions, etc.
        return e.message
    }
}
