# ESP-IDF project. STATUS: written against the ESP-IDF 5.x API but NEVER BUILT in this repository's
# development environment (no ESP-IDF toolchain was available). The portable core in components/*/src
# that does not include ESP-IDF headers IS compiled and tested on the host: see test/host.
cmake_minimum_required(VERSION 3.16)
set(EXTRA_COMPONENT_DIRS components)
include($ENV{IDF_PATH}/tools/cmake/project.cmake)
project(aerolab_controller)
