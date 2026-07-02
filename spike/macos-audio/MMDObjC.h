#import <Foundation/Foundation.h>

/// Runs `block`, catching any Objective-C NSException (e.g. AVFAudio's
/// "Failed to create tap due to format mismatch") and returning its reason
/// instead of letting it terminate the process. Swift cannot catch NSException
/// natively, so the mic tap (re)install goes through this: the mic is a
/// secondary channel and a mic failure must never crash the recorder and lose
/// the meeting's system audio.
BOOL MMDRunCatchingExceptions(NS_NOESCAPE void (^ _Nonnull block)(void),
                              NSString * _Nullable * _Nullable reason);
