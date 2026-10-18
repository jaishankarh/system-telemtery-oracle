package grouping

// The built-in App Profiles. To add a program, add a Profile to
// builtinProfiles (see profiles.go for what the fields mean) and a fixture
// process for it to scripts/gen-profiles-fixture.py. Order matters only where
// two Profiles could match one executable: the first wins (Chromium before
// Chrome, because the snap's Chromium binary is called "chrome").
//
// Command-line patterns are matched against the Process's full command line,
// so a path containing spaces cannot be matched by a leading `^\S*/`; such
// programs are found through their well-known flags instead.

// Shared role rules. Role rules are tried in order, first match wins.
var (
	// Flags Chromium and Electron put on their own helper processes.
	chromiumRoles = []RoleRule{
		{RoleCrashReporter, Recogniser{Cmd: `crashpad_handler|--type=crashpad-handler`}},
		{RoleExtension, Recogniser{Cmd: `--extension-process`}},
		{RoleZygote, Recogniser{Cmd: `--type=zygote`}},
		{RoleGPU, Recogniser{Cmd: `--type=gpu-process`}},
		{RoleRenderer, Recogniser{Cmd: `--type=renderer`}},
		{RoleUtility, Recogniser{Cmd: `--type=utility`}},
	}

	// What the VS Code family starts: the processes Settings Fixes for
	// "editor watchers and language servers" need to tell apart.
	editorRoles = concat([]RoleRule{
		{RolePtyHost, Recogniser{Cmd: `--type=ptyHost`}},
		{RoleExtensionHost, Recogniser{Cmd: `--type=extensionHost`}},
		{RoleSharedProcess, Recogniser{Cmd: `--type=sharedProcess`}},
		{RoleFileWatcher, Recogniser{Cmd: `--type=fileWatcher|(?i:watcher)|inotifywait|watchman|fsnotifier`}},
	}, chromiumRoles, []RoleRule{
		{RoleRipgrep, Recogniser{Comm: []string{"rg"}, Cmd: `(^|/)(rg|ripgrep)( |$)`}},
		{RoleLanguageServer, Recogniser{Cmd: languageServer}},
	})

	// What a JetBrains IDE starts.
	jetbrainsRoles = []RoleRule{
		{RoleFileWatcher, Recogniser{Cmd: `(^|/)fsnotifier( |$)`}},
		{RoleGradleDaemon, Recogniser{Cmd: `org\.gradle\.launcher\.daemon\.bootstrap\.GradleDaemon`}},
		{RoleKotlinDaemon, Recogniser{Cmd: `KotlinCompileDaemon`}},
		{RoleMavenServer, Recogniser{Cmd: `org\.jetbrains\.idea\.maven\.server|org\.mvndaemon|\bmvnd\b`}},
		{RoleRenderer, Recogniser{Cmd: `(^|/)jcef_helper( |$)`}},
		{RoleRipgrep, Recogniser{Comm: []string{"rg"}, Cmd: `(^|/)(rg|ripgrep)( |$)`}},
		{RoleLanguageServer, Recogniser{Cmd: languageServer}},
	}
)

// languageServer recognises language servers by program name or the protocol flag.
const languageServer = `(?i:language[-_ ]?server|languageserver|tsserver|typescript-language|rust-analyzer|gopls|pyright|pylance|jdtls|org\.eclipse\.jdt\.ls|clangd|omnisharp|solargraph|ruby-lsp|intelephense|terraform-ls|vscode-(json|css|html|markdown|eslint)|eslintserver|metals)|\blsp\b|--stdio`

func concat(parts ...[]RoleRule) []RoleRule {
	var out []RoleRule
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// electronEditor describes a VS Code family editor; installDirs are the
// directory names its files live under (/usr/share/<dir>/, /opt/<dir>/).
func electronEditor(key, name, exe, installDirs string) Profile {
	return Profile{
		Key: key, Name: name, Host: true,
		Roots: []Recogniser{{Cmd: `^\S*/(` + exe + `)( |$)|^\S*/(share|opt)/(` + installDirs + `)/`}},
		Roles: editorRoles, TerminalParentRoles: []string{RolePtyHost},
	}
}

// jetbrainsIDE describes one JetBrains IDE by the idea.paths.selector its launcher passes.
func jetbrainsIDE(key, name, selector string) Profile {
	return Profile{
		Key: key, Name: name, Host: true,
		Roots:               []Recogniser{{Cmd: `-Didea\.paths\.selector=(` + selector + `)`}},
		Roles:               jetbrainsRoles,
		TerminalParentRoles: []string{RoleMain},
	}
}

func chromiumBrowser(key, name, cmd string) Profile {
	return Profile{Key: key, Name: name, Roots: []Recogniser{{Cmd: cmd}}, Roles: chromiumRoles}
}

func builtinProfiles() []Profile {
	return []Profile{
		// Editors (VS Code family)
		electronEditor("cursor", "Cursor", `[Cc]ursor|[Cc]ursor\.AppImage`, `[Cc]ursor`),
		electronEditor("windsurf", "Windsurf", `[Ww]indsurf`, `[Ww]indsurf`),
		electronEditor("vscodium", "VSCodium", `codium|vscodium`, `codium|vscodium`),
		electronEditor("vscode", "Visual Studio Code", `code|code-insiders|code-oss|vscode`, `code|code-insiders|code-oss|vscode|visual-studio-code`),

		// JetBrains IDEs, each its own Application. The Gradle and Maven daemons they start belong to them.
		jetbrainsIDE("android-studio", "Android Studio", `AndroidStudio\w*`),
		jetbrainsIDE("intellij-idea", "IntelliJ IDEA", `IntelliJIdea\w*|IdeaIC\w*`),
		jetbrainsIDE("pycharm", "PyCharm", `PyCharm\w*`),
		jetbrainsIDE("webstorm", "WebStorm", `WebStorm\w*`),
		jetbrainsIDE("goland", "GoLand", `GoLand\w*`),
		jetbrainsIDE("clion", "CLion", `CLion\w*`),
		jetbrainsIDE("rider", "Rider", `Rider\w*`),
		jetbrainsIDE("phpstorm", "PhpStorm", `PhpStorm\w*`),
		jetbrainsIDE("rubymine", "RubyMine", `RubyMine\w*`),
		jetbrainsIDE("rustrover", "RustRover", `RustRover\w*`),
		jetbrainsIDE("datagrip", "DataGrip", `DataGrip\w*`),

		// Browsers. Chromium before Chrome: the snap's Chromium binary is called "chrome".
		chromiumBrowser("chromium", "Chromium", `^\S*/(chromium(-browser)?|chromium(-browser)?/[\w-]+)( |$)`),
		chromiumBrowser("chrome", "Google Chrome", `^\S*/(google/chrome/[\w-]+|google-chrome[\w-]*|app/chrome/[\w-]+)( |$)`),
		chromiumBrowser("brave", "Brave", `^\S*/brave(-browser[\w-]*)?( |$)`),
		chromiumBrowser("edge", "Microsoft Edge", `^\S*/(msedge|microsoft-edge[\w-]*)( |$)`),
		{
			Key: "firefox", Name: "Firefox",
			Roots: []Recogniser{{Cmd: `^\S*/firefox[\w-]*( |$)`}},
			Roles: []RoleRule{
				{RoleGPU, Recogniser{Cmd: `-contentproc\b.*\sgpu\s*$`}},
				{RoleUtility, Recogniser{Cmd: `-contentproc\b.*\s(rdd|socket|utility|forkserver|vr)\s*$|-forkserver`}},
				{RoleRenderer, Recogniser{Cmd: `-contentproc\b`}},
			},
		},

		// Android and Gradle. Both are their own Application unless an IDE (Gradle) hosts them.
		{
			Key: "android-emulator", Name: "Android emulator",
			Roots: []Recogniser{{Cmd: `/emulator/(qemu/[\w-]+/)?(emulator[\w-]*|qemu-system-[\w-]+)( |$)|^\S*/qemu-system-\S+ .*-avd `}},
			Roles: []RoleRule{
				{RoleCrashReporter, Recogniser{Cmd: `crash-monitor`}},
				{RoleEmulatorVM, Recogniser{Cmd: `qemu-system-`}},
			},
		},
		{
			Key: "gradle-daemon", Name: "Gradle daemon", Absorbable: true,
			Roots: []Recogniser{{Cmd: `org\.gradle\.launcher\.daemon\.bootstrap\.GradleDaemon|org\.jetbrains\.kotlin\.daemon\.KotlinCompileDaemon`}},
			Roles: []RoleRule{
				{RoleGradleDaemon, Recogniser{Cmd: `GradleDaemon`}},
				{RoleKotlinDaemon, Recogniser{Cmd: `KotlinCompileDaemon`}},
			},
		},
		{
			Key: "maven-daemon", Name: "Maven daemon", Absorbable: true,
			Roots: []Recogniser{{Cmd: `org\.mvndaemon\.mvnd\.daemon\.Server`}},
			Roles: []RoleRule{{RoleMavenServer, Recogniser{Cmd: `org\.mvndaemon`}}},
		},
	}
}
