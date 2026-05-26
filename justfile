default:
    @just --list

# Pull latest, rebuild + reinstall bridge, print version.
build:
    git pull
    make install-go
    bridge --version

# Run Go + shim tests.
test:
    make all
