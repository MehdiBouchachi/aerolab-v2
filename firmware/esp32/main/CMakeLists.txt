idf_component_register(SRCS "app_main.cpp" INCLUDE_DIRS "."
    REQUIRES board_profile safety controller protocol config_store message_buffer command_handler actuators sensors mqtt_client diagnostics clock nvs_flash esp_wifi esp_event esp_timer)
