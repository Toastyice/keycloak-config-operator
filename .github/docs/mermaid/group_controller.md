```mermaid
flowchart TD
    A[Reconcile Request] --> B[Fetch Group Object]
    B --> C{Group Found?}
    C -->|No| D[End - Group Deleted]
    C -->|Yes| E{Marked for Deletion?}
    
    %% Deletion Flow
    E -->|Yes| F[Get Keycloak Client for Deletion]
    F --> G{Keycloak Client Available?}
    G -->|No - Not Ready| H[Update Status: Deletion Pending<br/>Requeue 10s]
    G -->|Error| I[Return Error]
    G -->|Yes| J[Reconcile Delete Process]
    
    %% Main Reconciliation Flow
    E -->|No| K[Get Keycloak Client]
    K --> L{Keycloak Client Available?}
    L -->|No - Not Ready| M[Log & Requeue 5s<br/>No Status Update]
    L -->|Error| N[Update Status with Error<br/>Return]
    L -->|Yes| O[Ensure Finalizer]
    
    O --> P{Finalizer Exists?}
    P -->|No| Q[Add Finalizer & Update]
    P -->|Yes| R[Validate Realm]
    Q --> R
    
    %% Realm Validation
    R --> S[Get Referenced Realm Object]
    S --> T{Realm exists?}
    T -->|No| U[Update Status: Realm Not Found]
    T -->|Yes| V{Realm Ready?}
    V -->|No| W[Update Status: Realm Not Ready]
    V -->|Yes| X[Set Owner Reference]
    
    %% Owner Reference
    X --> Y{Correct Owner Ref Exists?}
    Y -->|Yes| Z[Continue to Group Reconciliation]
    Y -->|No| AA{Cross-Namespace Ref?}
    AA -->|Yes| BB[Skip Owner Ref - Log Warning]
    AA -->|No| CC[Set Owner Reference & Update]
    BB --> Z
    CC --> Z
    
    %% Main Group Reconciliation
    Z --> DD[Resolve Parent Group]
    DD --> EE{Parent Group Required?}
    EE -->|Yes| FF[Lookup Parent Group by Name]
    FF --> GG{Parent Found?}
    GG -->|No| HH[Update Status: Parent Not Found]
    GG -->|Yes| II[Store Parent UUID]
    EE -->|No| JJ[Continue without Parent]
    II --> JJ
    JJ --> KK[Get or Create Group in Keycloak]
    
    %% Group Creation/Update Logic
    KK --> LL{Group UUID Exists?}
    LL -->|No| MM[Create New Group]
    LL -->|Yes| NN[Fetch Existing Group]
    
    %% Fetch Existing Group
    NN --> OO{Group Found in Keycloak?}
    OO -->|404 - Not Found| PP[Clear Group State<br/>Create New Group]
    OO -->|Other Error| QQ[Update Status: Fetch Failed]
    OO -->|Found| RR[Compare Configuration]
    
    %% Create New Group
    MM --> SS[Build Group from Spec]
    PP --> SS
    SS --> TT[Create Group in Keycloak]
    TT --> UU{Creation Success?}
    UU -->|409 Conflict| VV[Manual Intervention Required]
    UU -->|Other Error| WW[Update Status: Creation Failed]
    UU -->|Success| XX[Fetch Created Group UUID]
    XX --> YY[Store UUID in Status]
    YY --> ZZ[Update Status: Group Created]
    
    %% Configuration Comparison
    RR --> AAA[Compare Group Fields:<br/>• name<br/>• parentId<br/>• realmRoles<br/>• clientRoles<br/>• attributes]
    AAA --> BBB{Differences Found?}
    BBB -->|No| CCC[Update Status: Group Synchronized]
    BBB -->|Yes| DDD[Log Configuration Changes]
    DDD --> EEE[Apply Changes to Group]
    EEE --> FFF[Update Group in Keycloak]
    FFF --> GGG{Update Success?}
    GGG -->|Error| HHH[Update Status: Update Failed]
    GGG -->|Success| III[Update Status: Group Updated]
    
    %% Deletion Process Details
    J --> JJJ{Group UUID Exists?}
    JJJ -->|No| KKK[Skip Keycloak Deletion]
    JJJ -->|Yes| LLL[Get Realm for Deletion]
    LLL --> MMM{Realm Found?}
    MMM -->|No| NNN[Skip Keycloak Deletion]
    MMM -->|Yes| OOO[Delete Group from Keycloak]
    
    OOO --> PPP{Deletion Success?}
    PPP -->|404 - Already Deleted| QQQ[Log: Already Deleted]
    PPP -->|Other Error| RRR[Requeue Deletion in 5s]
    PPP -->|Success| SSS[Log: Group Deleted]
    
    KKK --> TTT[Remove Finalizer]
    NNN --> TTT
    QQQ --> TTT
    SSS --> TTT
    TTT --> UUU[Update Object]
    UUU --> VVV[End - Object Will Be Removed]
    
    %% Status Update Process with Retry Logic
    ZZ --> WWW[Status Update with Retry]
    CCC --> WWW
    III --> WWW
    U --> WWW
    W --> WWW
    H --> WWW
    QQ --> WWW
    WW --> WWW
    HHH --> WWW
    VV --> WWW
    
    WWW --> XXX[Retry Up to 3 Times]
    XXX --> YYY{Status Update Success?}
    YYY -->|Conflict & Retries Left| ZZZ[Exponential Backoff<br/>Fetch Latest & Retry]
    YYY -->|Success| AAAA[Determine Requeue Interval]
    YYY -->|Failed After Retries| BBBB[Return Status Error]
    
    ZZZ --> XXX
    
    AAAA --> CCCC{Group Ready?}
    CCCC -->|No| DDDD[Requeue in 10s]
    CCCC -->|Yes| EEEE[Requeue in 30s for Periodic Sync]
    
    %% Parent Group Resolution Details
    FF --> FFFF[Search Groups in Realm by Name]
    FFFF --> GGGG{Multiple Groups Found?}
    GGGG -->|Yes| HHHH[Use First Match - Log Warning]
    GGGG -->|No| IIII[Use Single Match]
    HHHH --> IIII
    IIII --> II
    
    %% Field Comparison Details
    BBB --> JJJJ[Field Differences:<br/>• name: 'old' → 'new'<br/>• parentId: uuid1 → uuid2<br/>• realmRoles: [role1] → [role1,role2]<br/>• clientRoles: map changes<br/>• attributes: map changes]
    
    %% Special Cases & Error Handling
    style VV fill:#ffcdd2,stroke:#d32f2f,stroke-width:2px
    style RRR fill:#fff3e0,stroke:#f57c00,stroke-width:2px
    
    %% Styling
    classDef errorNode fill:#ffebee,stroke:#f44336,stroke-width:2px
    classDef successNode fill:#e8f5e8,stroke:#4caf50,stroke-width:2px
    classDef processNode fill:#e3f2fd,stroke:#2196f3,stroke-width:2px
    classDef decisionNode fill:#fff3e0,stroke:#ff9800,stroke-width:2px
    classDef statusNode fill:#f3e5f5,stroke:#9c27b0,stroke-width:2px
    classDef parentNode fill:#e0f2f1,stroke:#00695c,stroke-width:2px
    
    class I,N,QQ,WW,HHH,VV,BBBB errorNode
    class D,VVV,EEEE successNode
    class WWW,XXX,AAAA processNode
    class C,E,G,L,P,T,V,Y,AA,EE,GG,LL,OO,UU,BBB,GGG,JJJ,MMM,PPP,YYY,CCCC decisionNode
    class ZZ,CCC,III,U,W,H statusNode
    class DD,FF,II,FFFF,GGGG parentNode
```