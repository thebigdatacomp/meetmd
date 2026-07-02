#import "MMDObjC.h"

BOOL MMDRunCatchingExceptions(void (^block)(void), NSString **reason) {
    @try {
        block();
        return YES;
    } @catch (NSException *e) {
        if (reason) { *reason = e.reason ?: e.name; }
        return NO;
    }
}
