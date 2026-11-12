# Host build of the PORTABLE firmware core (no ESP-IDF needed). Compiles with strict warnings
# and AddressSanitizer + UBSan so memory/UB bugs in the safety code fail the build.
CC ?= gcc
C := ../../components
SRC := $(C)/controller/src/controller.c $(C)/protocol/src/protocol.c $(C)/protocol/src/jsonscan.c \
       $(C)/message_buffer/src/message_buffer.c $(C)/config_store/src/config_store.c $(C)/command_handler/src/command_handler.c \
       $(C)/board_profile/src/board_profile.c
INC := -I$(C)/controller/include -I$(C)/safety/include -I$(C)/protocol/include -I$(C)/message_buffer/include \
       -I$(C)/config_store/include -I$(C)/command_handler/include -I$(C)/board_profile/include
CFLAGS := -std=c11 -D_POSIX_C_SOURCE=200809L -O1 -g -Wall -Wextra -Werror -Wshadow -Wconversion -Wno-sign-conversion -fsanitize=address,undefined -fno-omit-frame-pointer $(INC)

build/fw_tests: test_main.c $(SRC)
	mkdir -p build
	$(CC) $(CFLAGS) -o $@ test_main.c $(SRC) -lm

.PHONY: test clean
test: build/fw_tests
	./build/fw_tests ../../../../contracts/examples build/out
clean:
	rm -rf build
