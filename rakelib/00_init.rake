# coding: utf-8

# Initialization
# This file contains the toolkits that
# aren't related to the source code.
# It means that they don't change very often
# and can be cached for later use.

require 'open3'

# Cross-platform way of finding an executable in the $PATH.
# Source: https://stackoverflow.com/a/5471032
#
#   which('ruby') #=> /usr/bin/ruby
def which(cmd)
    if File.executable?(cmd)
        return cmd
    end

    exts = ENV['PATHEXT'] ? ENV['PATHEXT'].split(';') : ['']
    ENV['PATH'].split(File::PATH_SEPARATOR).each do |path|
      exts.each do |ext|
        exe = File.join(path, "#{cmd}#{ext}")
        return exe if File.executable?(exe) && !File.directory?(exe)
      end
    end
    nil
end

# Returns true if the libc-musl variant of the libc library is used. Otherwise,
# returns false (the standard variant is used).
def detect_libc_musl()
    platform = Gem::Platform.local
    if platform.version.nil?
        return false
    end
    return platform.version == "musl"
end

# Searches for the tasks in the provided file
def find_tasks(file)
    tasks = []
    # Iterate over all tasks
    Rake.application.tasks.each do |t|
        # Choose only tasks from a specific file
        if t.actions.empty?
            next
        end
        action = t.actions[0]
        location, _ = action.source_location
        if location != file
            next
        end
        tasks.append t
    end
    return tasks
end

# Searches for the prerequisites tasks from the second provided file in the
# first provided file.
def find_prerequisites_tasks(source_tasks_file, prerequisites_file)
    require 'set'
    unique_prerequisites = Set[]

    # Choose only tasks from a specific file
    tasks = find_tasks(source_tasks_file)

    # Iterate over tasks
    tasks.each do |t|
        # Iterate over prerequisites
        t.all_prerequisite_tasks.each do |p|
            # Select unique prerequisites
            unique_prerequisites.add p
        end
    end

    prerequisites_tasks = []

    # Check the prerequisites
    unique_prerequisites.each do |p|
        # Check the location - accept only tasks from the init file
        if p.actions.empty?
            next
        end

        action = p.actions[0]
        location, _ = action.source_location

        if location == prerequisites_file
            prerequisites_tasks.append p
        end
    end

    return prerequisites_tasks
end

# Searches for the prerequisites from the init file in the provided file and
# invoke them.
def find_and_prepare_deps(file)
    if file == __FILE__
        prerequisites_tasks = find_tasks(file)
    else
        prerequisites_tasks = find_prerequisites_tasks(file, __FILE__)
    end

    prerequisites_tasks.each do |t|
        if t.class != Rake::FileTask
            next
        end
        print "Preparing: ", t, "...\n"
        t.invoke()
    end
end

# Searches for the prerequisites from the init file in the provided file and
# checks if they exist. It accepts the system-wide dependencies list and tests
# if they are in PATH.
def check_deps(file, *system_deps)
    def print_status(name, path, ok)
        status = "[ OK ]"
        if !ok
            status = "[MISS]"
        end

        if !path.nil?
            path = " (" + path + ")"
        end

        print status, " ", name, path, "\n"
    end

    if file == __FILE__
        prerequisites_tasks = find_tasks(file)
    else
        prerequisites_tasks = find_prerequisites_tasks(file, __FILE__)
    end

    manual_install_prerequisites_tasks = []
    prerequisites_tasks.each do |t|
        if t.instance_variable_get(:@manuall_install)
            manual_install_prerequisites_tasks.append t
        end
    end

    manual_install_prerequisites_tasks.each do |t|
        prerequisites_tasks.delete t
    end

    puts "Prerequisites:"
    prerequisites_tasks.sort_by{ |t| t.to_s().rpartition("/")[2] }.each do |t|
        if t.class != Rake::FileTask
            next
        end

        path = t.to_s
        name = path
        _, _, name = path.rpartition("/")

        print_status(name, path, File.exist?(path))
    end

    puts "System dependencies:"

    system_deps
        .map { |d| [d.to_s, which(d)] }
        .map { |d, path| [d, path, !path.nil?] }
        .concat(
            manual_install_prerequisites_tasks
            .map { |p| [p.to_s().rpartition("/")[2], p.to_s() ] }
            .map { |p, path| [p, path, File.exist?(path)] }
        )
        .sort_by{ |name, _, _| name }
        .each { |args| print_status(*args) }
end

# Defines the version guard for a file task. The version guard allows file
# tasks to depend on the version from the Rake variable. Using it for the tasks
# that have frozen versions using external files is not necessary.
# It accepts a task to be guarded and the version.
def add_version_guard(task_name, version)
    task = Rake::Task[task_name]
    if task.class != Rake::FileTask
        fail "file task required"
    end

    # We don't use the version guard for the prerequisities that must be
    # installed manually on current operating system
    if task.instance_variable_get(:@manuall_install)
        return
    end

    # The version stamp file is a prerequisite, but it is created after the
    # guarded task. It allows for cleaning the target directory in the task
    # body.
    version_stamp = "#{task_name}-#{version}.version"
    file version_stamp
    task.enhance [version_stamp] do
        # Removes old version stamps
        FileList["#{task_name}-*.version"].each do |f|
            FileUtils.rm f
        end
        # Creates a new version stamp with a timestamp before the guarded task
        # execution.
        FileUtils.touch [version_stamp], mtime: task.timestamp
    end 
end

# Defines a :phony task that you can use as a dependency. This allows
# file-based tasks to use non-file-based tasks as prerequisites
# without forcing them to rebuild.
# Adopted from: https://github.com/ruby/rake/blob/master/lib/rake/phony.rb
task :phony

Rake::Task[:phony].tap do |task|
  def task.timestamp # :nodoc:
    Time.at 0
  end
end

def require_manual_install_on(task_name, *conditions)
    task = nil
    if Rake::Task.task_defined? task_name
        task = Rake::Task[task_name]
    end

    # The task may not exist for the executables that must be found in PATH.
    # Other files must have assigned file tasks.
    if (!task.nil? && task.class != Rake::FileTask) || (task.nil? && task_name.include?("/"))
        fail "file task required"
    end

    if !conditions.any?
        if task.nil?
            # Create an empty file task to prevent failure due to a non-exist
            # file if the file isn't required.
            file task_name => [:phony]
        end
        return task_name
    end

    # Remove the self-installed task due to it is unsupported.
    if !task.nil?
        task.clear()
        Rake.application.instance_variable_get('@tasks').delete(task_name)
    end

    # Search in PATH for executable.
    program = File.basename task_name
    system_path = which(program)
    if !system_path.nil?
        program = system_path
    end

    # Create a new task that fails if the executable doesn't exist.
    file program do
        fail "#{newTask.to_s} must be installed manually on your operating system"
    end

    # Add a magic variable to indicate that it's a manually installed file.
    newTask = Rake::Task[program]
    newTask.instance_variable_set(:@manuall_install, true)

    return newTask.name
end

# Fetches the file from the network. You should add the WGET to the
# prerequisites of the task that uses this function.
# The file is saved in the target location.
def fetch_file(url, target)
    # extract wget version
    stdout, _, status = Open3.capture3(WGET, "--version")
    wget = [WGET]

    # BusyBox edition has no version switch and supports only basic features.
    if status == 0
        wget.append "--tries=inf", "--waitretry=3"
        wget_version = stdout.split("\n")[0]
        wget_version = wget_version[/[0-9]+\.[0-9]+/]
        # versions prior to 1.19 lack support for --retry-on-http-error
        if wget_version.empty? or wget_version >= "1.19"
            wget.append "--retry-on-http-error=429,500,503,504"
        end
    end

    if ENV["CI"] == "true" || target.nil?
        wget.append "-q"
    end

    wget.append url
    wget.append "-O", target

    sh *wget
end

### Recognize the operating system
uname=`uname -s`

case uname.rstrip
    when "Darwin"
        OS="macos"
    when "Linux"
        OS="linux"
    when "FreeBSD"
        OS="FreeBSD"
    when "OpenBSD"
        OS="OpenBSD"    
    else
        puts "ERROR: Unknown/unsupported OS: %s" % UNAME
        fail
end

### Tasks support conditions
# Some prerequisites are related to the libc library but
# without official libc-musl variants. They cannot be installed using this Rake
# script.
libc_musl_system = detect_libc_musl()
# Some prerequisites doesn't have a public packages for BSD-like operating
# systems.
freebsd_system = OS == "FreeBSD"
openbsd_system = OS == "OpenBSD"
any_system = true

### Define package versions
go_ver='1.18.8'
openapi_generator_ver='5.2.0'
goswagger_ver='v0.23.0'
protoc_ver='3.20.3'
protoc_gen_go_ver='v1.26.0'
protoc_gen_go_grpc_ver='v1.1.0'
richgo_ver='v0.3.10'
mockery_ver='v2.13.1'
mockgen_ver='v1.6.0'
golangcilint_ver='1.50.1'
yamlinc_ver='0.1.10'
node_ver='14.18.2'
dlv_ver='v1.8.3'
gdlv_ver='v1.8.0'
bundler_ver='2.3.8'
storybook_ver='6.5.14'

# System-dependent variables
case OS
when "macos"
  go_suffix="darwin-amd64"
  protoc_suffix="osx-x86_64"
  node_suffix="darwin-x64"
  golangcilint_suffix="darwin-amd64"
  chrome_drv_suffix="mac64"
  puts "WARNING: MacOS is not officially supported, the provisions for building on MacOS are made"
  puts "WARNING: for the developers' convenience only."
when "linux"
  go_suffix="linux-amd64"
  protoc_suffix="linux-x86_64"
  node_suffix="linux-x64"
  golangcilint_suffix="linux-amd64"
  chrome_drv_suffix="linux64"
when "FreeBSD"
  go_suffix="freebsd-amd64"
  golangcilint_suffix="freebsd-amd64"
when "OpenBSD"
else
  puts "ERROR: Unknown/unsupported OS: %s" % UNAME
  fail
end

### Define dependencies

# Directories
tools_dir = File.expand_path('tools')
directory tools_dir

node_dir = File.join(tools_dir, "nodejs")
directory node_dir

go_tools_dir = File.join(tools_dir, "golang")
gopath = File.join(go_tools_dir, "gopath")
directory go_tools_dir
directory gopath
file go_tools_dir => [gopath]

ruby_tools_dir = File.join(tools_dir, "ruby")
directory ruby_tools_dir

# We use the "bundle" gem to manage the dependencies. The "bundle" package is
# installed using the "gem" executable in the tools/ruby/gems directory, and 
# the link is created in the tools/ruby/bin directory. Next, Ruby dependencies
# are installed using the "bundle". It creates the tools/ruby/ruby/[VERSION]/
# directory with "bin" and "gems" subdirectories and uses these directories as
# the location of the installations. We want to avoid using a variadic Ruby
# version in the directory name. Therefore, we use the "binstubs" feature to
# create the links to the executable. Unfortunately, if we use the
# "tools/ruby/bin" directory as the target location then the "bundle"
# executable will be overridden and stop working. To work around this problem,
# we use two directories for Ruby binaries. The first contains the binaries
# installed using the "gem" command, and the second is a target for the
# "bundle" command.
ruby_tools_bin_dir = File.join(ruby_tools_dir, "bin")
directory ruby_tools_bin_dir
ruby_tools_bin_bundle_dir = File.join(ruby_tools_dir, "bin_bundle")
directory ruby_tools_bin_bundle_dir

# Automatically created directories by tools
ruby_tools_gems_dir = File.join(ruby_tools_dir, "gems")
goroot = File.join(go_tools_dir, "go")
gobin = File.join(goroot, "bin")
python_tools_dir = File.join(tools_dir, "python")
pythonpath = File.join(python_tools_dir, "lib")
node_bin_dir = File.join(node_dir, "bin")
protoc_dir = go_tools_dir

if libc_musl_system || openbsd_system
    gobin = ENV["GOBIN"]
    goroot = ENV["GOROOT"]
    if gobin.nil?
        gobin = which("go")
        if !gobin.nil?
            gobin = File.dirname gobin
        else
            gobin = ""
        end
    end
end

# Environment variables
ENV["GEM_HOME"] = ruby_tools_dir
ENV["BUNDLE_PATH"] = ruby_tools_dir
ENV["BUNDLE_BIN"] = ruby_tools_bin_bundle_dir
ENV["GOROOT"] = goroot
ENV["GOPATH"] = gopath
ENV["GOBIN"] = gobin
ENV["PATH"] = "#{node_bin_dir}:#{tools_dir}:#{gobin}:#{ENV["PATH"]}"
ENV["PYTHONPATH"] = pythonpath
ENV["VIRTUAL_ENV"] = python_tools_dir

### Detect Chrome
# CHROME_BIN is required for UI unit tests and system tests. If it is
# not provided by a user, try to locate Chrome binary and set
# environment variable to its location.
if !ENV['CHROME_BIN'] || ENV['CHROME_BIN'].empty?
    location = which("chromium")
    if location.nil?
        location = which("chrome")
    end
    if !location.nil?
        ENV['CHROME_BIN'] = location
    else    
        ENV['CHROME_BIN'] = "chromium"
        chrome_locations = []

        if OS == 'linux'
            chrome_locations = ['/usr/bin/chromium-browser', '/snap/bin/chromium', '/usr/bin/chromium']
        elsif OS == 'macos'
            chrome_locations = ["/Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome"]
        end
        # For each possible location check if the binary exists.
        chrome_locations.each do |loc|
            if File.exist?(loc)
                # Found Chrome binary.
                ENV['CHROME_BIN'] = loc
                break
            end
        end
    end
end
file ENV['CHROME_BIN']
CHROME = require_manual_install_on(ENV['CHROME_BIN'], any_system)

# System tools
WGET = require_manual_install_on("wget", any_system)
PYTHON3_SYSTEM = require_manual_install_on("python3", any_system)
JAVA = require_manual_install_on("java", any_system)
UNZIP = require_manual_install_on("unzip", any_system)
ENTR = require_manual_install_on("entr", any_system)
GIT = require_manual_install_on("git", any_system)
CREATEDB = require_manual_install_on("createdb", any_system)
PSQL = require_manual_install_on("psql", any_system)
DROPDB = require_manual_install_on("dropdb", any_system)
DROPUSER = require_manual_install_on("dropuser", any_system)
DOCKER = require_manual_install_on("docker", any_system)
DOCKER_COMPOSE = require_manual_install_on("docker-compose", any_system)
file DOCKER_COMPOSE => [DOCKER]
OPENSSL = require_manual_install_on("openssl", any_system)
GEM = require_manual_install_on("gem", any_system)
MAKE = require_manual_install_on("make", any_system)
GCC = require_manual_install_on("gcc", any_system)
TAR = require_manual_install_on("tar", any_system)
SED = require_manual_install_on("sed", any_system)
PERL = require_manual_install_on("perl", any_system)
FOLD = require_manual_install_on("fold", any_system)
SSH = require_manual_install_on("ssh", any_system)
SCP = require_manual_install_on("scp", any_system)
CLOUDSMITH = require_manual_install_on("cloudsmith", any_system)
ETAGS_CTAGS = require_manual_install_on("etags.ctags", any_system)
CLANG = require_manual_install_on("clang++", openbsd_system)

# Toolkits
BUNDLE = File.join(ruby_tools_bin_dir, "bundle")
file BUNDLE => [GEM, ruby_tools_dir, ruby_tools_bin_dir] do
    sh GEM, "install",
            "--minimal-deps",
            "--no-document",
            "--install-dir", ruby_tools_dir,
            "bundler:#{bundler_ver}"

    if !File.exists? BUNDLE
        # Workaround for old Ruby versions
        sh "ln", "-s", File.join(ruby_tools_gems_dir, "bundler-#{bundler_ver}", "exe", "bundler"), File.join(ruby_tools_bin_dir, "bundler")
        sh "ln", "-s", File.join(ruby_tools_gems_dir, "bundler-#{bundler_ver}", "exe", "bundle"), BUNDLE
    end

    sh BUNDLE, "--version"
end
add_version_guard(BUNDLE, bundler_ver)

fpm_gemfile = File.expand_path("init_deps/fpm.Gemfile", __dir__)
FPM = File.join(ruby_tools_bin_bundle_dir, "fpm")
file FPM => [BUNDLE, ruby_tools_dir, ruby_tools_bin_bundle_dir, fpm_gemfile] do
    sh BUNDLE, "install",
        "--gemfile", fpm_gemfile,
        "--path", ruby_tools_dir,
        "--binstubs", ruby_tools_bin_bundle_dir
    sh FPM, "--version"
end

danger_gemfile = File.expand_path("init_deps/danger.Gemfile", __dir__)
DANGER = File.join(ruby_tools_bin_bundle_dir, "danger")
file DANGER => [ruby_tools_bin_bundle_dir, ruby_tools_dir, danger_gemfile, BUNDLE] do
    sh BUNDLE, "install",
        "--gemfile", danger_gemfile,
        "--path", ruby_tools_dir,
        "--binstubs", ruby_tools_bin_bundle_dir
    sh "touch", "-c", DANGER
    sh DANGER, "--version"
end

npm = File.join(node_bin_dir, "npm")
file npm => [TAR, WGET, node_dir] do
    Dir.chdir(node_dir) do
        FileUtils.rm_rf(FileList["*"])
        fetch_file "https://nodejs.org/dist/v#{node_ver}/node-v#{node_ver}-#{node_suffix}.tar.xz", "node.tar.xz"
        sh TAR, "-Jxf", "node.tar.xz", "--strip-components=1"
        sh "rm", "node.tar.xz"
    end
    sh npm, "--version"
    sh "touch", "-c", npm
end
NPM = require_manual_install_on(npm, libc_musl_system, freebsd_system, openbsd_system)
add_version_guard(NPM, node_ver)

npx = File.join(node_bin_dir, "npx")
file npx => [NPM] do
    sh npx, "--version"
    sh "touch", "-c", npx
end
NPX = require_manual_install_on(npx, libc_musl_system)

YAMLINC = File.join(node_dir, "node_modules", "lib", "node_modules", "yamlinc", "bin", "yamlinc")
file YAMLINC => [NPM] do
    ci_opts = []
    if ENV["CI"] == "true"
        ci_opts += ["--no-audit", "--no-progress"]
    end

    sh NPM, "install",
            "-g",
            *ci_opts,
            "--prefix", "#{node_dir}/node_modules",
            "yamlinc@#{yamlinc_ver}"
    sh "touch", "-c", YAMLINC
    sh YAMLINC, "--version"
end
add_version_guard(YAMLINC, yamlinc_ver)

STORYBOOK = File.join(node_dir, "node_modules", "bin", "sb")
file STORYBOOK => [NPM] do
    ci_opts = []
    if ENV["CI"] == "true"
        ci_opts += ["--no-audit", "--no-progress"]
    end

    sh NPM, "install",
            "-g",
            *ci_opts,
            "--prefix", "#{node_dir}/node_modules",
            "storybook@#{storybook_ver}"
    sh "touch", "-c", STORYBOOK
    sh STORYBOOK, "--version"
end
add_version_guard(STORYBOOK, storybook_ver)

# Chrome driver is not currently used, but it can be needed in the UI tests.
# This file task is ready to use after uncomment.
#
# puts "WARNING: There are no chrome drv packages built for FreeBSD"
#
# CHROME_DRV = File.join(tools_dir, "chromedriver")
# file CHROME_DRV => [WGET, UNZIP, tools_dir] do
#     if !ENV['CHROME_BIN']
#         puts "Missing Chrome/Chromium binary. It is required for UI unit tests and system tests."
#         next
#     end

#     chrome_version = `"#{ENV['CHROME_BIN']}" --version | cut -d" " -f2 | tr -d -c 0-9.`
#     chrome_drv_version = chrome_version

#     if chrome_version.include? '85.'
#         chrome_drv_version = '85.0.4183.87'
#     elsif chrome_version.include? '86.'
#         chrome_drv_version = '86.0.4240.22'
#     elsif chrome_version.include? '87.'
#         chrome_drv_version = '87.0.4280.20'
#     elsif chrome_version.include? '90.'
#         chrome_drv_version = '90.0.4430.72'
#     elsif chrome_version.include? '92.'
#         chrome_drv_version = '92.0.4515.159'
#     elsif chrome_version.include? '93.'
#         chrome_drv_version = '93.0.4577.63'
#     elsif chrome_version.include? '94.'
#         chrome_drv_version = '94.0.4606.61' 
#     end

#     Dir.chdir(tools_dir) do
#         fetch_file "https://chromedriver.storage.googleapis.com/#{chrome_drv_version}/chromedriver_#{chrome_drv_suffix}.zip", "chromedriver.zip"
#         sh UNZIP, "-o", "chromedriver.zip"
#         sh "rm", "chromedriver.zip"
#     end

#     sh CHROME_DRV, "--version"
#     sh "chromedriver", "--version"  # From PATH
# end

OPENAPI_GENERATOR = File.join(tools_dir, "openapi-generator-cli.jar")
file OPENAPI_GENERATOR => [WGET, tools_dir] do
    fetch_file "https://repo1.maven.org/maven2/org/openapitools/openapi-generator-cli/#{openapi_generator_ver}/openapi-generator-cli-#{openapi_generator_ver}.jar", OPENAPI_GENERATOR
end
add_version_guard(OPENAPI_GENERATOR, openapi_generator_ver)

go = File.join(gobin, "go")
file go => [WGET, go_tools_dir] do
    Dir.chdir(go_tools_dir) do
        FileUtils.rm_rf("go")
        fetch_file "https://dl.google.com/go/go#{go_ver}.#{go_suffix}.tar.gz", "go.tar.gz"
        sh "tar", "-zxf", "go.tar.gz" 
        sh "rm", "go.tar.gz"
    end
    sh "touch", "-c", go
    sh go, "version"
end
GO = require_manual_install_on(go, libc_musl_system, openbsd_system)
add_version_guard(GO, go_ver)

GOSWAGGER = File.join(go_tools_dir, "goswagger")
file GOSWAGGER => [WGET, GO, TAR, go_tools_dir] do
    if OS != 'FreeBSD' && OS != "OpenBSD"
        goswagger_suffix = "linux_amd64"
        if OS == 'macos'
            # GoSwagger fails to build on macOS due to https://gitlab.isc.org/isc-projects/stork/-/issues/848.
            goswagger_suffix="darwin_amd64"
        end
        fetch_file "https://github.com/go-swagger/go-swagger/releases/download/#{goswagger_ver}/swagger_#{goswagger_suffix}", GOSWAGGER
        sh "chmod", "u+x", GOSWAGGER
    else
        # GoSwagger lacks the packages for BSD-like systems then it must be
        # built from sources.
        goswagger_archive = "#{GOSWAGGER}.tar.gz"
        goswagger_dir = "#{GOSWAGGER}-sources"
        sh "mkdir", goswagger_dir
        fetch_file "https://github.com/go-swagger/go-swagger/archive/refs/tags/#{goswagger_ver}.tar.gz", goswagger_archive
        sh TAR, "-zxf", goswagger_archive, "-C", goswagger_dir, "--strip-components=1"
        goswagger_build_dir = File.join(goswagger_dir, "cmd", "swagger")
        Dir.chdir(goswagger_build_dir) do
            sh GO, "build", "-ldflags=-X 'github.com/go-swagger/go-swagger/cmd/swagger/commands.Version=#{goswagger_ver}'"
        end
        sh "mv", File.join(goswagger_build_dir, "swagger"), GOSWAGGER
        sh "rm", "-rf", goswagger_dir
        sh "rm", goswagger_archive
    end

    sh "touch", "-c", GOSWAGGER
    sh GOSWAGGER, "version"
end
add_version_guard(GOSWAGGER, goswagger_ver)

protoc = File.join(protoc_dir, "protoc")
file protoc => [WGET, UNZIP, go_tools_dir] do
    Dir.chdir(go_tools_dir) do
        fetch_file "https://github.com/protocolbuffers/protobuf/releases/download/v#{protoc_ver}/protoc-#{protoc_ver}-#{protoc_suffix}.zip", "protoc.zip"
        sh UNZIP, "-o", "-j", "protoc.zip", "bin/protoc"
        sh "rm", "protoc.zip"
    end
    sh protoc, "--version"
    sh "touch", "-c", protoc
end
PROTOC = require_manual_install_on(protoc, libc_musl_system, freebsd_system, openbsd_system)
add_version_guard(PROTOC, protoc_ver)

PROTOC_GEN_GO = File.join(gobin, "protoc-gen-go")
file PROTOC_GEN_GO => [GO] do
    sh GO, "install", "google.golang.org/protobuf/cmd/protoc-gen-go@#{protoc_gen_go_ver}"
    sh PROTOC_GEN_GO, "--version"
end
add_version_guard(PROTOC_GEN_GO, protoc_gen_go_ver)

PROTOC_GEN_GO_GRPC = File.join(gobin, "protoc-gen-go-grpc")
file PROTOC_GEN_GO_GRPC => [GO] do
    sh GO, "install", "google.golang.org/grpc/cmd/protoc-gen-go-grpc@#{protoc_gen_go_grpc_ver}"
    sh PROTOC_GEN_GO_GRPC, "--version"
end
add_version_guard(PROTOC_GEN_GO_GRPC, protoc_gen_go_grpc_ver)

golangcilint = File.join(go_tools_dir, "golangci-lint")
file golangcilint => [WGET, GO, TAR, go_tools_dir] do
    Dir.chdir(go_tools_dir) do
        fetch_file "https://github.com/golangci/golangci-lint/releases/download/v#{golangcilint_ver}/golangci-lint-#{golangcilint_ver}-#{golangcilint_suffix}.tar.gz", "golangci-lint.tar.gz"
        sh "mkdir", "tmp"
        sh TAR, "-zxf", "golangci-lint.tar.gz", "-C", "tmp", "--strip-components=1"
        sh "mv", "tmp/golangci-lint", "."
        sh "rm", "-rf", "tmp"
        sh "rm", "-f", "golangci-lint.tar.gz"
    end
    sh golangcilint, "--version"
end
GOLANGCILINT = require_manual_install_on(golangcilint, openbsd_system)
add_version_guard(GOLANGCILINT, golangcilint_ver)

RICHGO = "#{gobin}/richgo"
file RICHGO => [GO] do
    sh GO, "install", "github.com/kyoh86/richgo@#{richgo_ver}"
    sh RICHGO, "version"
end
add_version_guard(RICHGO, richgo_ver)

MOCKERY = "#{gobin}/mockery"
file MOCKERY => [GO] do
    sh GO, "install", "github.com/vektra/mockery/v2@#{mockery_ver}"
    sh MOCKERY, "--version"
end
add_version_guard(MOCKERY, mockery_ver)

MOCKGEN = "#{gobin}/mockgen"
file MOCKGEN => [GO] do
    sh GO, "install", "github.com/golang/mock/mockgen@#{mockgen_ver}"
    sh MOCKGEN, "--version"
end
add_version_guard(MOCKGEN, mockgen_ver)

DLV = "#{gobin}/dlv"
file DLV => [GO] do
    sh GO, "install", "github.com/go-delve/delve/cmd/dlv@#{dlv_ver}"
    sh DLV, "version"
end
add_version_guard(DLV, dlv_ver)

GDLV = "#{gobin}/gdlv"
file GDLV => [GO] do
    sh GO, "install", "github.com/aarzilli/gdlv@#{gdlv_ver}"
    if !File.file?(GDLV)
        fail
    end
end
add_version_guard(GDLV, gdlv_ver)

PYTHON = File.join(python_tools_dir, "bin", "python")
file PYTHON => [PYTHON3_SYSTEM] do
    sh PYTHON3_SYSTEM, "-m", "venv", python_tools_dir
    sh PYTHON, "--version"
end

PIP = File.join(python_tools_dir, "bin", "pip")
file PIP => [PYTHON] do
    sh PYTHON, "-m", "ensurepip", "-U", "--default-pip"
    sh "touch", "-c", PIP
    sh PIP, "--version"
end

SPHINX_BUILD = File.expand_path("tools/python/bin/sphinx-build")
sphinx_requirements_file = File.expand_path("init_deps/sphinx.txt", __dir__)
file SPHINX_BUILD => [PIP, sphinx_requirements_file] do
    sh PIP, "install", "-r", sphinx_requirements_file
    sh "touch", "-c", SPHINX_BUILD
    sh SPHINX_BUILD, "--version"
end

PYTEST = File.expand_path("tools/python/bin/pytest")
pytests_requirements_file = File.expand_path("init_deps/pytest.txt", __dir__)
file PYTEST => [PIP, pytests_requirements_file] do
    sh PIP, "install", "-r", pytests_requirements_file
    sh "touch", "-c", PYTEST
    sh PYTEST, "--version"
end

#############
### Tasks ###
#############

desc 'Install all system-level dependencies'
task :prepare do
    find_and_prepare_deps(__FILE__)
end

desc 'Check all system-level dependencies'
task :check do
    check_deps(__FILE__)
end
