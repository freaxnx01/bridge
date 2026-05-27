default:
    @just --list

# Pull latest, rebuild + reinstall bridge (binary + shim), print version.
build:
    git pull
    make install
    bridge --version

# Run Go + shim tests.
test:
    make all
