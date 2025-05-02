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

import kotlin.system.exitProcess

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
        SourcesArgument(), // must come last
    )

fun main(args: Array<String>) {
    val opts = Options()
    ARGUMENT_PARSERS.forEach { it.setupDefault(opts) }

    if (!parseArgs(args, opts)) {
        exitProcess(-1)
    }

    println("compiling")
}

fun parseArgs(args: Array<String>, opts: Options): Boolean {
    var hasError = false
    var showHelp = args.isEmpty()
    val iter = args.iterator()
    while (iter.hasNext()) {
        val arg = iter.next()
        for (parser in ARGUMENT_PARSERS) {
            if (parser.matches(arg)) {
                if (parser is HelpArgument) {
                    showHelp = true
                }
                parser.parse(arg, iter, opts)
                if (parser.error != null) {
                    println("The error: " + parser.error)
                    hasError = true
                    System.err.println(parser.error)
                    System.err.println()
                }
                break
            }
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
