#include <stdio.h>
#include "esp_log.h"
#include "esp_spiffs.h"
#include "message_buffer.h"

/*
 * Journal payload backend: one small file per slot on the "journal" partition (wear-levelled
 * filesystem). Metadata stays in RAM (~400 x 100 B). Records are never written by the control
 * task: a storage task owns this backend.
 *
 * NOT BUILT/TESTED on a device. Cost to measure on real hardware: 360 snapshots + events per
 * 6 h outage must fit the partition and not wear it out (spec 14.4). On reboot the in-RAM
 * metadata is rebuilt by journal_fs_restore() (logic host-tested as mb_restore; this file I/O is not).
 */
static const char *TAG = "journal";
static int jput(void *ctx, int slot, const char *d, size_t n) {
    (void)ctx; char p[40]; snprintf(p, sizeof p, "/journal/j%03d", slot);
    FILE *f = fopen(p, "wb"); if (!f) return -1;
    size_t w = fwrite(d, 1, n, f); fclose(f); return w == n ? 0 : -1;
}
static int jget(void *ctx, int slot, char *b, size_t cap) {
    (void)ctx; char p[40]; snprintf(p, sizeof p, "/journal/j%03d", slot);
    FILE *f = fopen(p, "rb"); if (!f) return -1;
    size_t r = fread(b, 1, cap, f); fclose(f); return (int)r;
}
static void jdel(void *ctx, int slot) { (void)ctx; char p[40]; snprintf(p, sizeof p, "/journal/j%03d", slot); remove(p); }
const mb_backend_t MB_FS_BACKEND = {jput, jget, jdel};

int journal_fs_mount(void) {
    esp_vfs_spiffs_conf_t c = {.base_path = "/journal", .partition_label = "journal", .max_files = 8, .format_if_mount_failed = true};
    esp_err_t e = esp_vfs_spiffs_register(&c);
    if (e != ESP_OK) ESP_LOGE(TAG, "journal mount failed: %d (control continues; history is not buffered)", e);
    return e == ESP_OK ? 0 : -1;
}

/* Rebuild the RAM index from the files that survived a reboot; unreadable files are deleted (a counted gap). */
int journal_fs_restore(mb_t *m) {
    static char buf[1100];
    int restored = 0;
    for (int slot = 0; slot < m->cap; slot++) {
        int n = jget(NULL, slot, buf, sizeof buf);
        if (n <= 0) continue;
        if (mb_restore(m, slot, buf, (size_t)n) == 0) restored++;
        else { jdel(NULL, slot); m->overflow++; }
    }
    ESP_LOGI(TAG, "journal: %d buffered records restored after boot", restored);
    return restored;
}
