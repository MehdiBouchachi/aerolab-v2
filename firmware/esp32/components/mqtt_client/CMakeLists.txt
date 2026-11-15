idf_component_register(SRCS "src/net.c" INCLUDE_DIRS "include" REQUIRES mqtt protocol command_handler message_buffer controller clock diagnostics esp_wifi nvs_flash esp_timer)
