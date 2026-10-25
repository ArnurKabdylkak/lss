# Хостовые тесты логики, не зависящей от железа.
# Заголовки libopencm3 подменяются заглушками из fake/.
#
#   make -C test/host run

CFLAGS = -Wall -Wextra -O1 -I ../../include -I fake
ROOT = ../..

.PHONY: run clean

run: test_command test_code test_load
	./test_command
	./test_code
	./test_load

# Командный тракт стойки: смена кода, период отчётов, повторы и
# отказ от команд, адресованных другим устройствам.
test_command: test_command.c $(ROOT)/src/command.c $(ROOT)/src/code.c \
              fake/systime_stub.c
	$(CC) $(CFLAGS) -o $@ $^

# Проверка кода со стойки: время подменяется заглушкой.
test_code: test_code.c $(ROOT)/src/code.c fake/systime_stub.c
	$(CC) $(CFLAGS) -o $@ $^

# Счёт повторений ударного молота: геркон подменяется заглушкой GPIO,
# время — заглушкой таймера.
test_load: test_load.c $(ROOT)/src/load.c fake/gpio_stub.c fake/systime_stub.c
	$(CC) $(CFLAGS) -o $@ $^

clean:
	rm -f test_command test_code test_load
