//go:build darwin && cgo

package taskruntime

/*
#include <sys/types.h>
#include <sys/stat.h>
#include <sys/acl.h>
#include <fcntl.h>
#include <membership.h>
#include <uuid/uuid.h>
#include <errno.h>
#include <string.h>

// 0: owner-only/deny/absent; 1: non-owner allow; 2: query/classification failure.
// No path lookup, errno-as-absence fallback, or filesystem mutation.
static int canonical_acl_fd(int fd, uid_t owner) {
    filesec_t security = filesec_init();
    if (!security) return 2;
    struct stat st;
    int present = 0;
    if (fstatx_np(fd, &st, security) != 0 || st.st_uid != owner ||
        filesec_query_property(security, FILESEC_ACL, &present) != 0) {
        filesec_free(security);
        return 2;
    }
    if (!present) {
        filesec_free(security);
        return 0;
    }
    acl_t acl = NULL;
    int got = filesec_get_property(security, FILESEC_ACL, &acl);
    filesec_free(security);
    if (got != 0 || !acl || (void *)acl == _FILESEC_REMOVE_ACL) return 2;
    uuid_t owner_uuid;
    int result = 2;
    if (mbr_uid_to_uuid(owner, owner_uuid) != 0) goto done;
    for (int which = ACL_FIRST_ENTRY;; which = ACL_NEXT_ENTRY) {
        acl_entry_t entry;
        errno = 0;
        int rc = acl_get_entry(acl, which, &entry);
        if (rc == -1 && errno == EINVAL) { result = 0; break; }
        if (rc != 0) break;
        acl_tag_t tag;
        if (acl_get_tag_type(entry, &tag) != 0 ||
            (tag != ACL_EXTENDED_ALLOW && tag != ACL_EXTENDED_DENY)) break;
        void *qualifier = acl_get_qualifier(entry);
        if (!qualifier) break;
        int same = memcmp(qualifier, owner_uuid, sizeof(uuid_t)) == 0;
        if (acl_free(qualifier) != 0) break;
        if (tag == ACL_EXTENDED_ALLOW && !same) { result = 1; break; }
    }
done:
    if (acl_free(acl) != 0) result = 2;
    return result;
}
*/
import "C"

func canonicalACLStatus(fd int, owner uint32) int {
	return int(C.canonical_acl_fd(C.int(fd), C.uid_t(owner)))
}
